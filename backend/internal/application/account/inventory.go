package account

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/chenyme/grok2api/backend/internal/repository"
)

type inventoryRepository interface {
	ListInventoryGroups(context.Context) ([]repository.InventoryGroup, error)
	CreateInventoryGroup(context.Context, string) (repository.InventoryGroup, error)
	InventoryGroupByName(context.Context, string) (repository.InventoryGroup, error)
	StockAccounts(context.Context, []uint64, uint64, time.Time) error
	UnstockAccounts(context.Context, []uint64, time.Time) error
	SetQualityVerdict(context.Context, uint64, string) error
}

// InventoryTester runs one account through the live quality-hold rules.
// verdict is "ok" or "degraded". A non-nil error leaves the stored verdict unchanged.
type InventoryTester interface {
	TestInventoryAccount(ctx context.Context, accountID, nodeID uint64) (verdict string, err error)
}

type InventoryQualityItem struct {
	AccountID uint64 `json:"accountId,string"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
}

type InventoryQualityJob struct {
	Running bool                   `json:"running"`
	Total   int                    `json:"total"`
	Done    int                    `json:"done"`
	Items   []InventoryQualityItem `json:"items"`
}

type inventoryJobState struct {
	mu      sync.Mutex
	running bool
	total   int
	done    int
	items   []InventoryQualityItem
	tester  InventoryTester
}

func inventoryGroupExists(ctx context.Context, repo inventoryRepository, groupID uint64) error {
	groups, err := repo.ListInventoryGroups(ctx)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if group.ID == groupID {
			return nil
		}
	}
	return ErrInvalidInput
}

func (s *Service) inventory() (inventoryRepository, error) {
	repo, ok := s.accounts.(inventoryRepository)
	if !ok {
		return nil, ErrInvalidInput
	}
	return repo, nil
}

func (s *Service) SetInventoryTester(tester InventoryTester) {
	if s.inventoryJob == nil {
		s.inventoryJob = &inventoryJobState{}
	}
	s.inventoryJob.mu.Lock()
	s.inventoryJob.tester = tester
	s.inventoryJob.mu.Unlock()
}

func (s *Service) ListInventoryGroups(ctx context.Context) ([]repository.InventoryGroup, error) {
	repo, err := s.inventory()
	if err != nil {
		return nil, err
	}
	return repo.ListInventoryGroups(ctx)
}

func (s *Service) CreateInventoryGroup(ctx context.Context, name string) (repository.InventoryGroup, error) {
	repo, err := s.inventory()
	if err != nil {
		return repository.InventoryGroup{}, err
	}
	group, err := repo.CreateInventoryGroup(ctx, name)
	if errors.Is(err, repository.ErrInvalidRecord) {
		return repository.InventoryGroup{}, ErrInvalidInput
	}
	if errors.Is(err, repository.ErrConflict) {
		return repository.InventoryGroup{}, ErrConflict
	}
	return group, err
}

// StockAccounts moves the given accounts into a group. includeLinked also stocks
// linked accounts that are not already in inventory. Already stocked rows keep their group.
func (s *Service) StockAccounts(ctx context.Context, ids []uint64, groupID uint64, groupName string, includeLinked bool) error {
	repo, err := s.inventory()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return ErrInvalidInput
	}
	if groupID == 0 {
		name := groupName
		if name == "" {
			name = "Default"
		}
		group, findErr := repo.InventoryGroupByName(ctx, name)
		if errors.Is(findErr, repository.ErrNotFound) {
			group, findErr = repo.CreateInventoryGroup(ctx, name)
		}
		if errors.Is(findErr, repository.ErrInvalidRecord) {
			return ErrInvalidInput
		}
		if errors.Is(findErr, repository.ErrConflict) {
			group, findErr = repo.InventoryGroupByName(ctx, name)
		}
		if findErr != nil {
			return findErr
		}
		groupID = group.ID
	} else if err := inventoryGroupExists(ctx, repo, groupID); err != nil {
		return err
	}
	targets := ids
	if includeLinked {
		targets, err = s.expandLinkedIDs(ctx, ids)
		if err != nil {
			return err
		}
	}
	return repo.StockAccounts(ctx, targets, groupID, s.now())
}

// UnstockAccounts returns selected accounts to the schedulable pool.
// includeLinked also unstocks linked accounts that are currently in inventory.
func (s *Service) UnstockAccounts(ctx context.Context, ids []uint64, includeLinked bool) error {
	repo, err := s.inventory()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return ErrInvalidInput
	}
	targets := ids
	if includeLinked {
		targets, err = s.expandLinkedIDs(ctx, ids)
		if err != nil {
			return err
		}
	}
	return repo.UnstockAccounts(ctx, targets, s.now())
}

func (s *Service) expandLinkedIDs(ctx context.Context, ids []uint64) ([]uint64, error) {
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	var add func(id uint64) error
	add = func(id uint64) error {
		if id == 0 {
			return nil
		}
		if _, ok := seen[id]; ok {
			return nil
		}
		seen[id] = struct{}{}
		out = append(out, id)
		view, err := s.Get(ctx, id)
		if errors.Is(err, ErrNotFound) || errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, linked := range view.Credential.LinkedAccounts {
			if err := add(linked.ID); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range ids {
		if err := add(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) InventoryQualityJob() InventoryQualityJob {
	if s.inventoryJob == nil {
		return InventoryQualityJob{Items: []InventoryQualityItem{}}
	}
	s.inventoryJob.mu.Lock()
	defer s.inventoryJob.mu.Unlock()
	return s.inventoryJob.snapshot()
}

// StartInventoryQualityTest starts the single in-process batch. A running batch is left as-is.
func (s *Service) StartInventoryQualityTest(accountIDs, nodeIDs []uint64) (InventoryQualityJob, bool, error) {
	if s.inventoryJob == nil || s.inventoryJob.tester == nil {
		return InventoryQualityJob{}, false, ErrInvalidInput
	}
	if len(accountIDs) == 0 {
		return InventoryQualityJob{}, false, ErrInvalidInput
	}
	s.inventoryJob.mu.Lock()
	if s.inventoryJob.running {
		job := s.inventoryJob.snapshot()
		s.inventoryJob.mu.Unlock()
		return job, false, nil
	}
	items := make([]InventoryQualityItem, len(accountIDs))
	for i, id := range accountIDs {
		items[i] = InventoryQualityItem{AccountID: id, State: "pending"}
	}
	s.inventoryJob.running = true
	s.inventoryJob.total = len(items)
	s.inventoryJob.done = 0
	s.inventoryJob.items = items
	job := s.inventoryJob.snapshot()
	s.inventoryJob.mu.Unlock()
	nodes := append([]uint64(nil), nodeIDs...)
	go func() {
		defer func() {
			s.inventoryJob.mu.Lock()
			s.inventoryJob.running = false
			s.inventoryJob.mu.Unlock()
		}()
		s.runInventoryQualityTest(append([]uint64(nil), accountIDs...), nodes)
	}()
	return job, true, nil
}

func (s *Service) runInventoryQualityTest(accountIDs, nodeIDs []uint64) {
	repo, _ := s.inventory()
	for index, id := range accountIDs {
		name := ""
		nodeID := uint64(0)
		if view, err := s.Get(context.Background(), id); err == nil {
			name = view.Credential.Name
			nodeID = view.Credential.EgressNodeID
		}
		s.inventoryJob.setItem(index, name, "running", "")
		if len(nodeIDs) > 0 {
			nodeID = nodeIDs[rand.Intn(len(nodeIDs))]
		}
		if nodeID == 0 {
			s.inventoryJob.finishItem(index, "skipped", "无节点")
			continue
		}
		verdict, err := s.inventoryJob.tester.TestInventoryAccount(context.Background(), id, nodeID)
		if err != nil || (verdict != "ok" && verdict != "degraded") {
			detail := "测试失败"
			if err != nil {
				detail = err.Error()
			}
			s.inventoryJob.finishItem(index, "failed", detail)
			continue
		}
		if repo != nil {
			if err := repo.SetQualityVerdict(context.Background(), id, verdict); err != nil {
				s.inventoryJob.finishItem(index, "failed", "结果没写入")
				continue
			}
		}
		s.inventoryJob.finishItem(index, verdict, "")
	}
}

func (j *inventoryJobState) snapshot() InventoryQualityJob {
	items := append([]InventoryQualityItem(nil), j.items...)
	if items == nil {
		items = []InventoryQualityItem{}
	}
	return InventoryQualityJob{Running: j.running, Total: j.total, Done: j.done, Items: items}
}

func (j *inventoryJobState) setItem(index int, name, state, detail string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if index < 0 || index >= len(j.items) {
		return
	}
	if name != "" {
		j.items[index].Name = name
	}
	j.items[index].State = state
	j.items[index].Detail = detail
}

func (j *inventoryJobState) finishItem(index int, state, detail string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if index < 0 || index >= len(j.items) {
		return
	}
	j.items[index].State = state
	j.items[index].Detail = detail
	j.done++
}
