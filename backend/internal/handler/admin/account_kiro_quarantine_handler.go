package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// KiroQuarantineEntry 列表项（账号级或 (账号,模型) 级）。
type KiroQuarantineEntry struct {
	AccountID       int64  `json:"account_id"`
	Model           string `json:"model,omitempty"`
	NotBefore       string `json:"not_before"`
	RemainingMillis int64  `json:"remaining_ms"`
	Attempts        int    `json:"attempts,omitempty"`
}

// ListKiroQuarantine GET /admin/api/accounts/kiro/quarantine
//
// 返回当前所有生效的 Kiro 隔离条目（账号级 + (账号,模型) 级）。
func (h *AccountHandler) ListKiroQuarantine(c *gin.Context) {
	snapshots := service.SnapshotKiroQuarantine()
	out := make([]KiroQuarantineEntry, 0, len(snapshots))
	for _, s := range snapshots {
		out = append(out, KiroQuarantineEntry{
			AccountID:       s.AccountID,
			Model:           s.Model,
			NotBefore:       s.NotBefore.Format("2006-01-02T15:04:05Z07:00"),
			RemainingMillis: s.RemainingMillis,
			Attempts:        s.Attempts,
		})
	}
	response.Success(c, gin.H{"items": out, "total": len(out)})
}

// ClearKiroQuarantine DELETE /admin/api/accounts/kiro/:id/quarantine
//
// 清除指定账号的所有隔离记录（账号级 + 该账号下所有模型级）。
// 若 query 参数 model 非空，则仅清除 (账号, model) 维度的记录。
func (h *AccountHandler) ClearKiroQuarantine(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid account id")
		return
	}
	model := strings.TrimSpace(c.Query("model"))
	if model != "" {
		service.ClearKiroModelQuarantine(id, model)
		response.Success(c, gin.H{"cleared": "model", "account_id": id, "model": model})
		return
	}
	service.ClearKiroQuarantine(id)
	response.Success(c, gin.H{"cleared": "account", "account_id": id})
}

// 占位防止 net/http 未使用警告
var _ = http.StatusOK
