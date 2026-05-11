// Package service - Kiro usage / quota probe
//
// 查询 Kiro 账号的当前用量与配额，使用 Kiro WebPortal 的 CBOR 协议。
//
// 端点：
//
//	POST https://app.kiro.dev/service/KiroWebPortalService/operation/GetUserUsageAndLimits
//	Headers:
//	  Authorization: Bearer {accessToken}
//	  Content-Type:  application/cbor
//	  Accept:        application/cbor
//	  smithy-protocol: rpc-v2-cbor
//	  Cookie:        Idp={provider}; AccessToken={accessToken}
//	  User-Agent:    KiroIDE-0.6.18-{machineId}
//	Body:           CBOR encoded {} (空 map)
//
// 响应：CBOR encoded usage 数据，结构与 kiro-account-manager `usageData` 完全一致
// （参见 kiro-accounts-1-2026-05-10.json 样例）。
//
// 错误语义复用 kiro_token_service 的 sentinel：
//
//	401 -> ErrKiroAuthFailed (调用方应触发 RefreshAccountToken 后重试)
//	423 / 403+TemporarilySuspended -> ErrKiroBanned
//	429 -> ErrKiroRateLimited
package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/fxamacker/cbor/v2"
)

const (
	kiroPortalUsageURL = "https://app.kiro.dev/service/KiroWebPortalService/operation/GetUserUsageAndLimits"
	kiroPortalTimeout  = 30 * time.Second
)

// kiroCBORDecMode 强制把所有 map 解为 map[string]any（默认会解为 map[any]any，
// 后续无法 json.Marshal、也无法用 map[string]any 类型断言）。
var kiroCBORDecMode cbor.DecMode

func init() {
	mode, err := cbor.DecOptions{
		DefaultMapType: reflect.TypeOf(map[string]any{}),
	}.DecMode()
	if err != nil {
		panic(fmt.Sprintf("kiro: build cbor dec mode: %v", err))
	}
	kiroCBORDecMode = mode
}

// KiroUsageService 查询 Kiro 账号的配额。无状态。
type KiroUsageService struct{}

// NewKiroUsageService 构造器。
func NewKiroUsageService() *KiroUsageService {
	return &KiroUsageService{}
}

// ProbeAccountUsage 返回上游 usage 数据（与 Account.Extra.kiro_usage_data 同结构）。
//
// 注意：调用方负责提前确保 access_token 有效。本函数不会主动触发刷新；
// 401 会返回 ErrKiroAuthFailed，由上层决定是否 refresh + retry。
func (s *KiroUsageService) ProbeAccountUsage(ctx context.Context, account *Account, proxyURL string) (map[string]any, error) {
	if account == nil || !account.IsKiro() {
		return nil, fmt.Errorf("kiro: account is nil or not a Kiro account")
	}
	accessToken := account.KiroAccessToken()
	if accessToken == "" {
		return nil, fmt.Errorf("kiro: account has no access_token; refresh first")
	}
	machineID := account.KiroMachineID()
	if machineID == "" {
		return nil, fmt.Errorf("kiro: account has no machine_id")
	}
	provider := account.KiroProvider()
	if provider == "" {
		// Cookie 里 Idp 字段缺失也可能正常，但留个默认值更稳
		provider = "BuilderId"
	}

	// 上游约定：请求 body 是空对象（CBOR map(0)）
	body, err := cbor.Marshal(struct{}{})
	if err != nil {
		return nil, fmt.Errorf("kiro usage: encode cbor body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kiroPortalUsageURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("kiro usage: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Accept", "application/cbor")
	req.Header.Set("smithy-protocol", "rpc-v2-cbor")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Cookie", fmt.Sprintf("Idp=%s; AccessToken=%s", provider, accessToken))
	req.Header.Set("User-Agent", fmt.Sprintf(KiroIDEUserAgentTmpl, machineID))

	respBody, status, err := doKiroPortalHTTP(ctx, req, proxyURL)
	if err != nil {
		return nil, err
	}
	if mapped := mapKiroStatusErr(status, respBody); mapped != nil {
		return nil, mapped
	}

	var data map[string]any
	if err := kiroCBORDecMode.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("kiro usage: decode cbor response: %w", err)
	}
	if data == nil {
		return map[string]any{}, nil
	}
	return data, nil
}

// doKiroPortalHTTP 与 doKiroHTTP 的区别仅在于不强制 ValidateResolvedIP（CBOR 端点可能走 CloudFront）。
func doKiroPortalHTTP(_ context.Context, req *http.Request, proxyURL string) ([]byte, int, error) {
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:           strings.TrimSpace(proxyURL),
		Timeout:            kiroPortalTimeout,
		ValidateResolvedIP: true,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("kiro usage: build http client: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("kiro usage: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	const maxBody = 4 << 20 // 4MB
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(chunk)
		if n > 0 {
			if len(buf)+n > maxBody {
				return nil, resp.StatusCode, fmt.Errorf("kiro usage: response too large")
			}
			buf = append(buf, chunk[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	return buf, resp.StatusCode, nil
}

// ApplyKiroUsageData 把 ProbeAccountUsage 返回的数据写入 Account.Extra（不修改原 map）。
// 返回新的 Extra 副本，调用方负责持久化。
func ApplyKiroUsageData(account *Account, usage map[string]any) map[string]any {
	if account == nil {
		return nil
	}
	extra := make(map[string]any, len(account.Extra)+2)
	for k, v := range account.Extra {
		extra[k] = v
	}
	if usage != nil {
		extra["kiro_usage_data"] = usage
	}
	extra["kiro_usage_probed_at"] = time.Now().Unix()
	return extra
}
