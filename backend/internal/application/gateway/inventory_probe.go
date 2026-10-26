package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	egressapp "github.com/chenyme/grok2api/backend/internal/application/egress"
	"github.com/chenyme/grok2api/backend/internal/domain/audit"
	modeldomain "github.com/chenyme/grok2api/backend/internal/domain/model"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
)

const defaultInventoryProbePrompt = "先用三点总结为什么天空呈蓝色，最后一行只输出 QUALITY_OK。"

func (s *Service) SetInventoryProbe(input egressapp.QualityProbeInput) {
	if s == nil {
		return
	}
	s.inventoryProbeClientKeyID = input.ClientKeyID
	s.inventoryProbeModel = strings.TrimSpace(input.Model)
	s.inventoryProbePrompt = strings.TrimSpace(input.Prompt)
	s.inventoryProbeMaxTokens = input.MaxOutputTokens
}

// TestInventoryAccount runs one stocked account through the live quality hold.
// The request id does not use the quality_ prefix, so the audit stays in the
// account trend and call history. Transport failures return an error and do
// not choose a verdict.
func (s *Service) TestInventoryAccount(ctx context.Context, accountID, nodeID uint64) (string, error) {
	if s == nil || s.inventoryProbeClientKeyID == 0 || accountID == 0 || nodeID == 0 {
		return "", egressapp.ErrInvalidInput
	}
	accountView, err := s.accounts.Get(ctx, accountID)
	if err != nil {
		return "", err
	}
	key, err := s.clientKeys.Get(ctx, s.inventoryProbeClientKeyID)
	if err != nil {
		return "", err
	}
	modelName := s.inventoryProbeModel
	if modelName == "" {
		modelName = "grok-4.6"
	}
	publicModel, ok := modeldomain.NormalizePublicID(accountView.Credential.Provider, modelName)
	if !ok {
		return "", egressapp.ErrInvalidInput
	}
	prompt := s.inventoryProbePrompt
	if prompt == "" {
		prompt = defaultInventoryProbePrompt
	}
	maxTokens := s.inventoryProbeMaxTokens
	if maxTokens <= 0 {
		maxTokens = 384
	}
	token, err := security.NewOpaqueToken(12)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"model": modelName, "messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream": true, "stream_options": map[string]bool{"include_usage": true}, "max_tokens": maxTokens,
	})
	if err != nil {
		return "", err
	}
	result, err := s.CreateChatCompletion(withInventoryProbe(ctx, accountID), Input{
		RequestID: "stockq_" + token, ClientKey: key, PublicModel: publicModel, Body: body,
		Streaming: true, Operation: audit.OperationChat,
		ForcedEgressNodeID: nodeID, ForcedAccountID: accountID,
		holdDespiteForcedNode: true, skipQualityPenalty: true, allowBoundNodeOverride: true,
	})
	if err != nil {
		var failure *UpstreamFailure
		if errors.As(err, &failure) && failure.Code == ErrorQualityDegraded {
			return "degraded", nil
		}
		return "", err
	}
	defer result.Body.Close()
	defer result.Finalize(Usage{}, "", "")
	_, _ = io.Copy(io.Discard, result.Body)
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return "", egressapp.ErrInvalidInput
	}
	return "ok", nil
}
