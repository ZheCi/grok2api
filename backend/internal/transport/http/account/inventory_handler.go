package account

import (
	"errors"
	"net/http"
	"strconv"

	accountapp "github.com/chenyme/grok2api/backend/internal/application/account"
	"github.com/chenyme/grok2api/backend/internal/shared/response"
	"github.com/gin-gonic/gin"
)

func queryUint(c *gin.Context, key string) uint64 {
	value, err := strconv.ParseUint(c.Query(key), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func (h *Handler) listInventoryGroups(c *gin.Context) {
	groups, err := h.service.ListInventoryGroups(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "inventoryGroupsFailed", "读取库存分组失败")
		return
	}
	response.Success(c, http.StatusOK, gin.H{"items": groups})
}

type inventoryGroupRequest struct {
	Name string `json:"name"`
}

func (h *Handler) createInventoryGroup(c *gin.Context) {
	var request inventoryGroupRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidInput", "分组名称无效")
		return
	}
	group, err := h.service.CreateInventoryGroup(c.Request.Context(), request.Name)
	if errors.Is(err, accountapp.ErrInvalidInput) {
		response.Error(c, http.StatusBadRequest, "invalidInput", "分组名称无效")
		return
	}
	if errors.Is(err, accountapp.ErrConflict) {
		response.Error(c, http.StatusConflict, "inventoryGroupExists", "分组已存在")
		return
	}
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "inventoryGroupFailed", "创建库存分组失败")
		return
	}
	response.Success(c, http.StatusOK, group)
}

type inventoryMoveRequest struct {
	IDs           []string `json:"ids"`
	GroupID       string   `json:"groupId"`
	GroupName     string   `json:"groupName"`
	IncludeLinked bool     `json:"includeLinked"`
}

func (h *Handler) stockAccounts(c *gin.Context) {
	var request inventoryMoveRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidInput", "入库参数无效")
		return
	}
	ids, err := parseIDs(request.IDs)
	if err != nil || len(ids) == 0 {
		response.Error(c, http.StatusBadRequest, "invalidInput", "入库账号无效")
		return
	}
	groupID, _ := strconv.ParseUint(request.GroupID, 10, 64)
	if err := h.service.StockAccounts(c.Request.Context(), ids, groupID, request.GroupName, request.IncludeLinked); err != nil {
		if errors.Is(err, accountapp.ErrInvalidInput) {
			response.Error(c, http.StatusBadRequest, "invalidInput", "入库参数无效")
			return
		}
		response.Error(c, http.StatusInternalServerError, "inventoryStockFailed", "账号入库失败")
		return
	}
	response.Success(c, http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) unstockAccounts(c *gin.Context) {
	var request inventoryMoveRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidInput", "移出参数无效")
		return
	}
	ids, err := parseIDs(request.IDs)
	if err != nil || len(ids) == 0 {
		response.Error(c, http.StatusBadRequest, "invalidInput", "移出账号无效")
		return
	}
	if err := h.service.UnstockAccounts(c.Request.Context(), ids, request.IncludeLinked); err != nil {
		if errors.Is(err, accountapp.ErrInvalidInput) {
			response.Error(c, http.StatusBadRequest, "invalidInput", "移出参数无效")
			return
		}
		response.Error(c, http.StatusInternalServerError, "inventoryUnstockFailed", "账号移出库存失败")
		return
	}
	response.Success(c, http.StatusOK, gin.H{"ok": true})
}

type inventoryQualityRequest struct {
	IDs     []string `json:"ids"`
	NodeIDs []string `json:"nodeIds"`
}

func (h *Handler) startInventoryQuality(c *gin.Context) {
	var request inventoryQualityRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidInput", "降智测试参数无效")
		return
	}
	ids, err := parseIDs(request.IDs)
	if err != nil || len(ids) == 0 {
		response.Error(c, http.StatusBadRequest, "invalidInput", "降智测试账号无效")
		return
	}
	nodeIDs, err := parseIDs(request.NodeIDs)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalidInput", "测试节点无效")
		return
	}
	job, started, err := h.service.StartInventoryQualityTest(ids, nodeIDs)
	if errors.Is(err, accountapp.ErrInvalidInput) {
		response.Error(c, http.StatusBadRequest, "invalidInput", "降智测试还不能开始")
		return
	}
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "inventoryQualityFailed", "降智测试失败")
		return
	}
	response.Success(c, http.StatusOK, gin.H{"started": started, "job": job})
}

func (h *Handler) currentInventoryQuality(c *gin.Context) {
	response.Success(c, http.StatusOK, gin.H{"job": h.service.InventoryQualityJob()})
}
