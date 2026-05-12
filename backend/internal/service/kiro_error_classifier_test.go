package service

import (
	"net/http"
	"testing"
)

func TestClassifyKiroError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   KiroErrorClass
	}{
		// === ThrottlingException ===
		{
			name:   "INSUFFICIENT_MODEL_CAPACITY (user real case)",
			status: 429,
			body:   `{"message":"I am experiencing high traffic, please try again shortly.","reason":"INSUFFICIENT_MODEL_CAPACITY"}`,
			want:   KiroErrModelCapacity,
		},
		{
			name:   "ThrottlingException + INSUFFICIENT_MODEL_CAPACITY full schema",
			status: 429,
			body:   `{"__type":"com.amazon.coral.service#ThrottlingException","message":"high traffic","reason":"INSUFFICIENT_MODEL_CAPACITY"}`,
			want:   KiroErrModelCapacity,
		},
		{
			name:   "ThrottlingException + DAILY_REQUEST_COUNT",
			status: 429,
			body:   `{"__type":"ThrottlingException","reason":"DAILY_REQUEST_COUNT","message":"daily limit"}`,
			want:   KiroErrAccountQuotaDaily,
		},
		{
			name:   "ThrottlingException + MONTHLY_REQUEST_COUNT",
			status: 429,
			body:   `{"__type":"ThrottlingException","reason":"MONTHLY_REQUEST_COUNT","message":"monthly limit"}`,
			want:   KiroErrAccountQuotaMonthly,
		},

		// === ServiceQuotaExceededException ===
		{
			name:   "ServiceQuotaExceeded + MONTHLY",
			status: 402,
			body:   `{"__type":"ServiceQuotaExceededException","reason":"MONTHLY_REQUEST_COUNT"}`,
			want:   KiroErrAccountQuotaMonthly,
		},
		{
			name:   "ServiceQuotaExceeded + OVERAGE",
			status: 402,
			body:   `{"__type":"ServiceQuotaExceededException","reason":"OVERAGE_REQUEST_LIMIT_EXCEEDED"}`,
			want:   KiroErrAccountQuotaMonthly,
		},
		{
			name:   "ServiceQuotaExceeded + CONVERSATION_LIMIT",
			status: 402,
			body:   `{"__type":"ServiceQuotaExceededException","reason":"CONVERSATION_LIMIT_EXCEEDED"}`,
			want:   KiroErrConversationTooLong,
		},

		// === AccessDeniedException ===
		{
			name:   "AccessDenied + TEMPORARILY_SUSPENDED",
			status: 403,
			body:   `{"__type":"AccessDeniedException","reason":"TEMPORARILY_SUSPENDED","message":"account temporarily is suspended"}`,
			want:   KiroErrAccountSuspended,
		},
		{
			name:   "AccessDenied + FEATURE_NOT_SUPPORTED",
			status: 403,
			body:   `{"__type":"AccessDeniedException","reason":"FEATURE_NOT_SUPPORTED"}`,
			want:   KiroErrAccessDenied,
		},

		// === ValidationException ===
		{
			name:   "Validation + INVALID_MODEL_ID",
			status: 400,
			body:   `{"__type":"ValidationException","reason":"INVALID_MODEL_ID","message":"unknown model"}`,
			want:   KiroErrInvalidRequest,
		},
		{
			name:   "Validation + CONTENT_LENGTH_EXCEEDS_THRESHOLD",
			status: 400,
			body:   `{"__type":"ValidationException","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`,
			want:   KiroErrInvalidRequest,
		},

		// === Auth ===
		{
			name:   "401 ExpiredToken plain text",
			status: 401,
			body:   `ExpiredTokenException: The security token included in the request is expired`,
			want:   KiroErrAuth,
		},
		{
			name:   "401 invalid bearer token",
			status: 401,
			body:   `{"message":"Bearer token included in the request is invalid"}`,
			want:   KiroErrAuth,
		},

		// === EventStream / 非结构化兜底（Phase 3）===
		{
			name:   "EventStream frame with INSUFFICIENT_MODEL_CAPACITY only",
			status: 200, // EventStream 错误帧 HTTP 仍是 200
			body:   `event:error\nmessage: INSUFFICIENT_MODEL_CAPACITY high traffic`,
			want:   KiroErrModelCapacity,
		},
		{
			name:   "improperly formed plain",
			status: 400,
			body:   `Improperly formed request payload`,
			want:   KiroErrInvalidRequest,
		},
		{
			name:   "temporarily is suspended plain",
			status: 403,
			body:   `Your account temporarily is suspended due to violation`,
			want:   KiroErrAccountSuspended,
		},

		// === HTTP status 兜底（Phase 4）===
		{
			name:   "raw 429 no body",
			status: 429,
			body:   ``,
			want:   KiroErrModelCapacity,
		},
		{
			name:   "raw 503",
			status: 503,
			body:   `<html>...gateway timeout</html>`,
			want:   KiroErrTransient,
		},
		{
			name:   "raw 413",
			status: 413,
			body:   `payload too large`,
			want:   KiroErrInvalidRequest,
		},
		{
			name:   "raw 402 no body",
			status: 402,
			body:   ``,
			want:   KiroErrAccountQuotaMonthly,
		},
		{
			name:   "raw 502 transient",
			status: http.StatusBadGateway,
			body:   ``,
			want:   KiroErrTransient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyKiroError(tt.status, []byte(tt.body))
			if got != tt.want {
				t.Errorf("ClassifyKiroError(%d, %q) = %s, want %s",
					tt.status, tt.body, got, tt.want)
			}
		})
	}
}
