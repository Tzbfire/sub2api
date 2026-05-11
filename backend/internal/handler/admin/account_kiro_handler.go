package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// kiroImportItem 与 kiro-account-manager 的导出 JSON 结构对齐（字段全部 camelCase）。
// 缺失字段允许空字符串，不强校验：上游同一份 JSON 中 IdC/Social 字段集合不同。
type kiroImportItem struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Label        string `json:"label"`
	AuthMethod   string `json:"authMethod"` // "Social" / "IdC"
	Provider     string `json:"provider"`   // Google / Github / BuilderId / Enterprise
	UserID       string `json:"userId"`
	MachineID    string `json:"machineId"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	IDToken      string `json:"idToken"`
	ExpiresAt    string `json:"expiresAt"` // 上游为字符串日期，仅展示用，不解析

	// IdC 专属
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Region       string `json:"region"`

	// Social 专属
	ProfileArn string `json:"profileArn"`
	StartUrl   string `json:"startUrl"`

	// 完整 usageData 透传到 Account.Extra["kiro_usage_data"]
	UsageData map[string]any `json:"usageData"`
}

// KiroImportRequest is the body of POST /admin/api/accounts/kiro/import
type KiroImportRequest struct {
	Items                 []kiroImportItem `json:"items" binding:"required"`
	GroupIDs              []int64          `json:"group_ids,omitempty"`
	Concurrency           int              `json:"concurrency,omitempty"`
	SkipMixedChannelCheck bool             `json:"skip_mixed_channel_check,omitempty"`
}

// KiroImportResult 单条导入结果
type KiroImportResult struct {
	Index   int    `json:"index"`
	ID      string `json:"id,omitempty"`
	Email   string `json:"email,omitempty"`
	Created bool   `json:"created"`
	Error   string `json:"error,omitempty"`
}

// ImportKiro 处理 kiro-account-manager 导出的 JSON 数组批量导入。
// POST /admin/api/accounts/kiro/import
//
// 入参格式：{"items": [<kiro-account-manager Account>, ...], "group_ids": []int64, ...}
// 出参：{"results": [...], "summary": {"total": N, "succeeded": M, "failed": N-M}}
func (h *AccountHandler) ImportKiro(c *gin.Context) {
	var req KiroImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.Items) == 0 {
		response.BadRequest(c, "items must not be empty")
		return
	}

	results := make([]KiroImportResult, 0, len(req.Items))
	succeeded := 0

	for i, item := range req.Items {
		res := KiroImportResult{Index: i, ID: item.ID, Email: item.Email}
		acc, err := h.createKiroAccountFromImport(c, &item, &req)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Created = true
			succeeded++
			if acc != nil {
				res.Email = acc.KiroEmail()
			}
		}
		results = append(results, res)
	}

	response.Success(c, gin.H{
		"results": results,
		"summary": gin.H{
			"total":     len(req.Items),
			"succeeded": succeeded,
			"failed":    len(req.Items) - succeeded,
		},
	})
}

func (h *AccountHandler) createKiroAccountFromImport(c *gin.Context, item *kiroImportItem, req *KiroImportRequest) (*service.Account, error) {
	authMethod := strings.ToLower(strings.TrimSpace(item.AuthMethod))
	if authMethod == "" {
		// 默认 Social；IdC 必须显式标
		authMethod = service.KiroAuthMethodSocial
	}
	if strings.HasPrefix(authMethod, "idc") || strings.Contains(authMethod, "identity") {
		authMethod = service.KiroAuthMethodIdC
	} else {
		authMethod = service.KiroAuthMethodSocial
	}

	if strings.TrimSpace(item.RefreshToken) == "" {
		return nil, errImportField("refreshToken is required")
	}
	if strings.TrimSpace(item.MachineID) == "" {
		return nil, errImportField("machineId is required (Kiro 后端用 machineId 鉴别请求来源)")
	}
	if authMethod == service.KiroAuthMethodIdC {
		if strings.TrimSpace(item.ClientID) == "" || strings.TrimSpace(item.ClientSecret) == "" {
			return nil, errImportField("IdC accounts require clientId + clientSecret")
		}
	}

	creds := map[string]any{
		"auth_method":   authMethod,
		"provider":      strings.TrimSpace(item.Provider),
		"email":         strings.TrimSpace(item.Email),
		"user_id":       strings.TrimSpace(item.UserID),
		"machine_id":    strings.TrimSpace(item.MachineID),
		"access_token":  strings.TrimSpace(item.AccessToken),
		"refresh_token": strings.TrimSpace(item.RefreshToken),
		"id_token":      strings.TrimSpace(item.IDToken),
	}
	if authMethod == service.KiroAuthMethodIdC {
		region := strings.TrimSpace(item.Region)
		if region == "" {
			region = service.KiroDefaultRegion
		}
		creds["client_id"] = strings.TrimSpace(item.ClientID)
		creds["client_secret"] = strings.TrimSpace(item.ClientSecret)
		creds["region"] = region
	} else {
		if v := strings.TrimSpace(item.ProfileArn); v != "" {
			creds["profile_arn"] = v
		}
		if v := strings.TrimSpace(item.StartUrl); v != "" {
			creds["start_url"] = v
		}
	}

	extra := map[string]any{
		"kiro_imported_at":    nowUnix(),
		"kiro_imported_label": strings.TrimSpace(item.Label),
	}
	if item.UsageData != nil {
		extra["kiro_usage_data"] = item.UsageData
	}

	name := strings.TrimSpace(item.Label)
	if name == "" {
		name = strings.TrimSpace(item.Email)
	}
	if name == "" {
		name = "kiro-" + strings.TrimSpace(item.ID)
	}

	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	input := &service.CreateAccountInput{
		Name:                  name,
		Platform:              service.PlatformKiro,
		Type:                  service.AccountTypeOAuth,
		Credentials:           creds,
		Extra:                 extra,
		Concurrency:           concurrency,
		GroupIDs:              req.GroupIDs,
		SkipMixedChannelCheck: req.SkipMixedChannelCheck,
	}

	return h.adminService.CreateAccount(c.Request.Context(), input)
}

// RefreshKiroUsage 拉取并写回 Kiro 账号的 usage 数据
// POST /admin/api/accounts/:id/kiro-usage
func (h *AccountHandler) RefreshKiroUsage(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	ctx := c.Request.Context()
	account, err := h.adminService.GetAccount(ctx, accountID)
	if err != nil || account == nil {
		response.NotFound(c, "Account not found")
		return
	}
	if !account.IsKiro() {
		response.BadRequest(c, "Account is not a Kiro account")
		return
	}

	tokenSvc := service.NewKiroTokenService()
	usageSvc := service.NewKiroUsageService()

	// 401 => refresh + retry
	usage, probeErr := usageSvc.ProbeAccountUsage(ctx, account, "")
	if probeErr != nil && errors.Is(probeErr, service.ErrKiroAuthFailed) {
		tokenInfo, refreshErr := tokenSvc.RefreshAccountToken(ctx, account, "")
		if refreshErr != nil {
			response.Error(c, http.StatusUnauthorized, "refresh failed: "+refreshErr.Error())
			return
		}
		account.Credentials = service.ApplyKiroTokenInfo(account, tokenInfo)
		// 立即持久化新 token，避免后续 probe 仍用旧 token
		if _, upErr := h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{
			Credentials: account.Credentials,
		}); upErr != nil {
			response.ErrorFrom(c, upErr)
			return
		}
		usage, probeErr = usageSvc.ProbeAccountUsage(ctx, account, "")
	}
	if probeErr != nil {
		response.Error(c, http.StatusBadGateway, "probe failed: "+probeErr.Error())
		return
	}

	newExtra := service.ApplyKiroUsageData(account, usage)
	updated, err := h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{
		Credentials: account.Credentials,
		Extra:       newExtra,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"usage":   usage,
		"capped":  updated.KiroIsCapped(),
		"account": h.buildAccountResponseWithRuntime(ctx, updated),
	})
}

// refreshKiroUsageOne 内部复用：刷新单个 Kiro 账号的 usage。
// 返回 (account, usage, error)。401 自动 refresh token 重试。
func (h *AccountHandler) refreshKiroUsageOne(ctx context.Context, account *service.Account) (*service.Account, map[string]any, error) {
	if account == nil || !account.IsKiro() {
		return account, nil, fmt.Errorf("not a kiro account")
	}
	tokenSvc := service.NewKiroTokenService()
	usageSvc := service.NewKiroUsageService()

	usage, probeErr := usageSvc.ProbeAccountUsage(ctx, account, "")
	if probeErr != nil && errors.Is(probeErr, service.ErrKiroAuthFailed) {
		tokenInfo, refreshErr := tokenSvc.RefreshAccountToken(ctx, account, "")
		if refreshErr != nil {
			return account, nil, refreshErr
		}
		account.Credentials = service.ApplyKiroTokenInfo(account, tokenInfo)
		if _, upErr := h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{
			Credentials: account.Credentials,
		}); upErr != nil {
			return account, nil, upErr
		}
		usage, probeErr = usageSvc.ProbeAccountUsage(ctx, account, "")
	}
	if probeErr != nil {
		return account, nil, probeErr
	}
	newExtra := service.ApplyKiroUsageData(account, usage)
	updated, err := h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{
		Credentials: account.Credentials,
		Extra:       newExtra,
	})
	if err != nil {
		return account, usage, err
	}
	return updated, usage, nil
}

// BatchRefreshKiroUsageRequest 批量刷新请求体。
type BatchRefreshKiroUsageRequest struct {
	AccountIDs []int64 `json:"account_ids"` // 空则刷新所有 Kiro 账号
}

// BatchRefreshKiroUsage 批量刷新 Kiro 账号 usage。
// POST /admin/api/accounts/kiro/batch-refresh-usage
func (h *AccountHandler) BatchRefreshKiroUsage(c *gin.Context) {
	var req BatchRefreshKiroUsageRequest
	_ = c.ShouldBindJSON(&req)

	ctx := c.Request.Context()
	var targets []*service.Account

	if len(req.AccountIDs) > 0 {
		for _, id := range req.AccountIDs {
			acct, err := h.adminService.GetAccount(ctx, id)
			if err == nil && acct != nil && acct.IsKiro() {
				targets = append(targets, acct)
			}
		}
	} else {
		all, _, err := h.adminService.ListAccounts(ctx, 1, 1000, service.PlatformKiro, "", "", "", 0, "", "", "")
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		for i := range all {
			a := all[i]
			targets = append(targets, &a)
		}
	}

	type itemResult struct {
		AccountID int64  `json:"account_id"`
		Name      string `json:"name"`
		OK        bool   `json:"ok"`
		Error     string `json:"error,omitempty"`
		Capped    bool   `json:"capped,omitempty"`
	}
	results := make([]itemResult, 0, len(targets))
	successCount := 0
	for _, acct := range targets {
		updated, _, err := h.refreshKiroUsageOne(ctx, acct)
		r := itemResult{AccountID: acct.ID, Name: acct.Name}
		if err != nil {
			r.Error = err.Error()
		} else {
			r.OK = true
			r.Capped = updated.KiroIsCapped()
			successCount++
		}
		results = append(results, r)
	}
	response.Success(c, gin.H{
		"total":   len(targets),
		"success": successCount,
		"failed":  len(targets) - successCount,
		"results": results,
	})
}

// ============== helpers ==============

type importErr string

func (e importErr) Error() string     { return string(e) }
func errImportField(msg string) error { return importErr(msg) }

func nowUnix() int64 { return time.Now().Unix() }
