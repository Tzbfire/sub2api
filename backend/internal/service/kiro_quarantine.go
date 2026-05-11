// Kiro 账号限流隔离（in-memory 短期 cooldown）。
//
// Kiro 上游 429/423/throttling 时，立即把账号放进黑名单 N 分钟，
// 期间 SelectKiroAccount 跳过它，避免重试风暴。
//
// 设计要点：
//   - 完全 in-memory（sync.Map）：进程重启自动清空，符合"短期限流"语义
//   - 默认 cooldown：429 / throttling → 5min；423 banned → 30min；其他 4xx/5xx → 60s
//   - 独立于 RateLimitService 的 temp_unschedulable_rules（那个需要每账号配置规则）
package service

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	kiroQuarantineDefault = 60 * time.Second
	kiroQuarantine429     = 5 * time.Minute
	kiroQuarantine423     = 30 * time.Minute
	kiroQuarantineAuth    = 5 * time.Minute
)

var kiroQuarantineMap sync.Map // accountID(int64) → notBefore(time.Time)

// IsKiroQuarantined 检查账号是否处于 cooldown。已过期会顺手清理。
func IsKiroQuarantined(accountID int64) bool {
	v, ok := kiroQuarantineMap.Load(accountID)
	if !ok {
		return false
	}
	notBefore, ok := v.(time.Time)
	if !ok {
		kiroQuarantineMap.Delete(accountID)
		return false
	}
	if time.Now().After(notBefore) {
		kiroQuarantineMap.Delete(accountID)
		return false
	}
	return true
}

// QuarantineKiroAccount 把账号放入隔离区，duration 为持续时间。
func QuarantineKiroAccount(accountID int64, duration time.Duration) {
	if duration <= 0 {
		return
	}
	kiroQuarantineMap.Store(accountID, time.Now().Add(duration))
}

// QuarantineKiroFromUpstreamError 根据上游状态码与响应体推断 cooldown 时长。
// 5xx（除 503/529）与网络错误不隔离 — 通常是上游临时抖动，不该惩罚账号。
func QuarantineKiroFromUpstreamError(accountID int64, statusCode int, body []byte) time.Duration {
	bodyLower := strings.ToLower(string(body))
	var dur time.Duration
	switch {
	case statusCode == http.StatusTooManyRequests, // 429
		strings.Contains(bodyLower, "throttl"),
		strings.Contains(bodyLower, "rate limit"),
		strings.Contains(bodyLower, "quota"),
		strings.Contains(bodyLower, "monthly_request_count"),
		strings.Contains(bodyLower, "request_limit_exceeded"):
		dur = kiroQuarantine429
	case statusCode == http.StatusLocked, // 423
		strings.Contains(bodyLower, "banned"),
		strings.Contains(bodyLower, "suspend"):
		dur = kiroQuarantine423
	case statusCode == http.StatusUnauthorized,
		statusCode == http.StatusForbidden:
		dur = kiroQuarantineAuth
	case statusCode == http.StatusServiceUnavailable, // 503
		statusCode == 529:
		dur = kiroQuarantineDefault
	default:
		// 4xx 其他、5xx 网络抖动等：不隔离，仅本次请求切换账号
		return 0
	}
	QuarantineKiroAccount(accountID, dur)
	return dur
}

// ClearKiroQuarantine 立即解除隔离（admin UI 操作 / 手动 refresh 成功后调用）。
func ClearKiroQuarantine(accountID int64) {
	kiroQuarantineMap.Delete(accountID)
}
