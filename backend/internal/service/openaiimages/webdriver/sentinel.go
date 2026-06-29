package webdriver

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	pkglogger "github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// sentinel.go：抓 chatgpt.com 首页解析 SDK 资源 + chat-requirements 握手。

var (
	scriptSrcRe = regexp.MustCompile(`<script[^>]+src="(https?://[^"]+)"`)
	dataBuildRe = regexp.MustCompile(`data-build="([^"]+)"`)
	sdkPathRe   = regexp.MustCompile(`/c/[^/]*/_`)
)

const bootstrapTTL = 5 * time.Minute

type bootstrapEntry struct {
	scripts   []string
	dataBuild string
	expiry    time.Time
}

var (
	bootstrapMu    sync.RWMutex
	bootstrapCache *bootstrapEntry
)

func loadBootstrap() ([]string, string, bool) {
	bootstrapMu.RLock()
	defer bootstrapMu.RUnlock()
	if bootstrapCache == nil || time.Now().After(bootstrapCache.expiry) {
		return nil, "", false
	}
	scripts := append([]string(nil), bootstrapCache.scripts...)
	return scripts, bootstrapCache.dataBuild, true
}

func storeBootstrap(scripts []string, dataBuild string) {
	bootstrapMu.Lock()
	defer bootstrapMu.Unlock()
	bootstrapCache = &bootstrapEntry{
		scripts:   append([]string(nil), scripts...),
		dataBuild: dataBuild,
		expiry:    time.Now().Add(bootstrapTTL),
	}
}

// ResetBootstrapCacheForTest 测试辅助：重置 sentinel 资源缓存。
func ResetBootstrapCacheForTest() {
	bootstrapMu.Lock()
	bootstrapCache = nil
	bootstrapMu.Unlock()
}

// bootstrap 预热 chatgpt.com 并解析 sentinel SDK 资源。失败安全：返回兜底。
func bootstrap(ctx context.Context, client *HTTPClient, headers http.Header, baseURL string) ([]string, string) {
	if scripts, db, ok := loadBootstrap(); ok && baseURL == startURL {
		return scripts, db
	}
	resp, err := client.R().
		SetContext(ctx).
		SetHeaders(headerToMap(headers)).
		DisableAutoReadResponse().
		Get(baseURL)
	if err != nil || resp == nil || resp.Body == nil {
		if err != nil {
			pkglogger.L().Warn("openaiimages.bootstrap_transport_failed",
				zap.String("url", baseURL),
				zap.String("error", err.Error()),
			)
		}
		return []string{defaultSentinelSDKURL}, ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()

	// chatgpt.com 首页非 2xx 时，body 通常是 Cloudflare 挑战 HTML，
	// 既无法解析出 sentinel script，也意味着拿不到 cf_clearance Cookie。
	// 后续 backend-api 调用必然 403。这里显式记录 CF-Ray / Server / 状态码，
	// 直接退回兜底且不缓存，让上层基于真实失败决策（继续或换号）。
	if !resp.IsSuccessState() {
		preview := string(body)
		if len(preview) > 400 {
			preview = preview[:400]
		}
		pkglogger.L().Warn("openaiimages.bootstrap_non_success",
			zap.String("url", baseURL),
			zap.Int("status", resp.StatusCode),
			zap.String("cf_ray", resp.Header.Get("CF-Ray")),
			zap.String("cf_mitigated", resp.Header.Get("CF-Mitigated")),
			zap.String("server", resp.Header.Get("Server")),
			zap.String("content_type", resp.Header.Get("Content-Type")),
			zap.String("body_preview", preview),
		)
		return []string{defaultSentinelSDKURL}, ""
	}

	html := string(body)
	var scripts []string
	for _, m := range scriptSrcRe.FindAllStringSubmatch(html, -1) {
		src := m[1]
		if strings.Contains(src, "chatgpt.com") || strings.Contains(src, "127.0.0.1") || strings.Contains(src, "localhost") {
			scripts = append(scripts, src)
		}
	}
	if len(scripts) == 0 {
		scripts = []string{defaultSentinelSDKURL}
	}

	dataBuild := ""
	if m := dataBuildRe.FindStringSubmatch(html); len(m) > 1 {
		dataBuild = m[1]
	}
	if dataBuild == "" {
		for _, s := range scripts {
			if m := sdkPathRe.FindString(s); m != "" {
				dataBuild = m
				break
			}
		}
	}
	if baseURL == startURL {
		storeBootstrap(scripts, dataBuild)
	}
	return scripts, dataBuild
}

// initConversation 调 /backend-api/conversation/init。失败不阻塞主流程（与上游 web 行为一致）。
func initConversation(ctx context.Context, client *HTTPClient, headers http.Header, baseURL string) error {
	h := withTargetPath(headers, targetPathOf(baseURL))
	resp, err := client.R().
		SetContext(ctx).
		SetHeaders(headerToMap(h)).
		SetBodyJsonMarshal(map[string]any{
			"gizmo_id":                nil,
			"requested_default_model": nil,
			"conversation_id":         nil,
			"timezone_offset_min":     tzOffsetMinutes(),
			"system_hints":            []string{"picture_v2"},
		}).
		Post(baseURL)
	if err != nil {
		return &TransportError{Wrapped: err}
	}
	if !resp.IsSuccessState() {
		return classifyHTTPError(resp, "conversation init failed")
	}
	return nil
}

// fetchChatRequirements 拿 sentinel token + PoW 参数。
//
// ChatGPT Web 新版 sentinel 协议为两步握手：
//  1. /chat-requirements/prepare  提交 p(requirements token)，拿 prepare_token + PoW challenge
//  2. /chat-requirements/finalize 提交 prepare_token + proof_token，拿最终 token
//
// 为了兼容旧测试环境/旧上游，prepare/finalize 不可用时回退到旧的
// /chat-requirements 单端点协议。
func fetchChatRequirements(
	ctx context.Context,
	client *HTTPClient,
	headers http.Header,
	baseURL string,
	scriptSources []string,
	dataBuild string,
) (*chatRequirements, error) {
	ua := headers.Get("User-Agent")
	reqToken := buildRequirementsToken(ua, scriptSources, dataBuild)

	if result, err := fetchChatRequirementsV2(ctx, client, headers, baseURL, reqToken, ua, scriptSources, dataBuild); err == nil {
		return result, nil
	} else if !shouldFallbackToLegacyRequirements(err) {
		return nil, err
	} else {
		pkglogger.L().Warn("openaiimages.chat_requirements_v2_fallback",
			zap.String("error", err.Error()),
		)
	}

	return fetchChatRequirementsLegacy(ctx, client, headers, baseURL, reqToken)
}

func fetchChatRequirementsV2(
	ctx context.Context,
	client *HTTPClient,
	headers http.Header,
	baseURL string,
	reqToken string,
	ua string,
	scriptSources []string,
	dataBuild string,
) (*chatRequirements, error) {
	base := strings.TrimRight(baseURL, "/")
	prepareURL := base + "/prepare"
	finalizeURL := base + "/finalize"

	var prepare struct {
		chatRequirements
		PrepareToken string `json:"prepare_token"`
	}
	prepareHeaders := withTargetPath(headers, targetPathOf(prepareURL))
	prepareHeaders.Set("Content-Type", "application/json")
	resp, err := client.R().
		SetContext(ctx).
		SetHeaders(headerToMap(prepareHeaders)).
		SetBodyJsonMarshal(map[string]any{"p": reqToken}).
		SetSuccessResult(&prepare).
		Post(prepareURL)
	if err != nil {
		return nil, &TransportError{Wrapped: err}
	}
	if !resp.IsSuccessState() {
		logChatRequirementsHTTPFailure("prepare", 0, resp)
		return nil, classifyHTTPError(resp, "chat-requirements prepare failed")
	}

	// 把 challenge 类结果直接交给 caller 分类，避免在这里丢失 arkose/turnstile 细节。
	if prepare.Arkose.Required || prepare.Turnstile.Required {
		out := prepare.chatRequirements
		return &out, nil
	}
	if strings.TrimSpace(prepare.PrepareToken) == "" {
		return nil, errors.New("chat-requirements prepare missing prepare_token")
	}

	proofToken, err := buildProofToken(
		prepare.ProofOfWork.Required,
		prepare.ProofOfWork.Seed,
		prepare.ProofOfWork.Difficulty,
		ua,
		scriptSources,
		dataBuild,
	)
	if err != nil {
		return nil, &ProtocolError{Reason: err.Error()}
	}

	var result chatRequirements
	finalizeHeaders := withTargetPath(headers, targetPathOf(finalizeURL))
	finalizeHeaders.Set("Content-Type", "application/json")
	resp, err = client.R().
		SetContext(ctx).
		SetHeaders(headerToMap(finalizeHeaders)).
		SetBodyJsonMarshal(map[string]any{
			"prepare_token":   prepare.PrepareToken,
			"proof_token":     proofToken,
			"turnstile_token": "",
		}).
		SetSuccessResult(&result).
		Post(finalizeURL)
	if err != nil {
		return nil, &TransportError{Wrapped: err}
	}
	if !resp.IsSuccessState() {
		logChatRequirementsHTTPFailure("finalize", 0, resp)
		return nil, classifyHTTPError(resp, "chat-requirements finalize failed")
	}
	if strings.TrimSpace(result.Token) == "" {
		return nil, errors.New("chat-requirements finalize missing token")
	}
	result.ProofToken = proofToken
	return &result, nil
}

func shouldFallbackToLegacyRequirements(err error) bool {
	if err == nil {
		return false
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return false
	}
	var au *AuthError
	if errors.As(err, &au) {
		return false
	}
	var pe *ProtocolError
	if errors.As(err, &pe) {
		return false
	}
	// 老服务/测试桩没有 /prepare 时，classifyHTTPError 返回普通 error；允许回退。
	return true
}

func fetchChatRequirementsLegacy(
	ctx context.Context,
	client *HTTPClient,
	headers http.Header,
	baseURL string,
	reqToken string,
) (*chatRequirements, error) {
	payloads := []map[string]any{
		{"p": reqToken},
		{"p": nil},
	}
	reqHeaders := withTargetPath(headers, targetPathOf(baseURL))
	var lastErr error
	for i, payload := range payloads {
		var result chatRequirements
		resp, err := client.R().
			SetContext(ctx).
			SetHeaders(headerToMap(reqHeaders)).
			SetBodyJsonMarshal(payload).
			SetSuccessResult(&result).
			Post(baseURL)
		if err != nil {
			lastErr = &TransportError{Wrapped: err}
			continue
		}
		if resp.IsSuccessState() {
			return &result, nil
		}
		logChatRequirementsHTTPFailure("legacy", i, resp)
		lastErr = classifyHTTPError(resp, "chat-requirements failed")
	}
	if lastErr == nil {
		lastErr = errors.New("chat-requirements failed")
	}
	return nil, lastErr
}

func logChatRequirementsHTTPFailure(stage string, attempt int, resp *HTTPResponse) {
	if resp == nil {
		return
	}
	body, _ := resp.ToBytes()
	bodyPreview := string(body)
	if len(bodyPreview) > 600 {
		bodyPreview = bodyPreview[:600]
	}
	pkglogger.L().Warn("openaiimages.chat_requirements_failed",
		zap.String("stage", stage),
		zap.Int("attempt", attempt),
		zap.Int("status", resp.StatusCode),
		zap.String("cf_ray", resp.Header.Get("CF-Ray")),
		zap.String("server", resp.Header.Get("Server")),
		zap.String("content_type", resp.Header.Get("Content-Type")),
		zap.String("body_preview", bodyPreview),
	)
}
