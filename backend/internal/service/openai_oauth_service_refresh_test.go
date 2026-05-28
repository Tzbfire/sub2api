package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type openaiOAuthClientRefreshStub struct {
	refreshCalls    int32
	refreshResponse *openai.TokenResponse
	refreshErr      error
}

func (s *openaiOAuthClientRefreshStub) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientRefreshStub) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.refreshCalls, 1)
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientRefreshStub) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.refreshCalls, 1)
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	if s.refreshResponse != nil {
		return s.refreshResponse, nil
	}
	return nil, errors.New("not implemented")
}

func TestOpenAIOAuthService_RefreshAccountToken_NoRefreshTokenUsesExistingAccessToken(t *testing.T) {
	client := &openaiOAuthClientRefreshStub{}
	svc := NewOpenAIOAuthService(nil, client)

	expiresAt := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
	account := &Account{
		ID:       77,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "existing-access-token",
			"expires_at":   expiresAt,
			"client_id":    "client-id-1",
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "existing-access-token", info.AccessToken)
	require.Equal(t, "client-id-1", info.ClientID)
	require.Zero(t, atomic.LoadInt32(&client.refreshCalls), "existing access token should be reused without calling refresh")
}

func TestOpenAIOAuthService_RefreshAccountToken_FallsBackToImageAccountPlan(t *testing.T) {
	client := &openaiOAuthClientRefreshStub{
		refreshResponse: &openai.TokenResponse{
			AccessToken:  "new-access-token",
			RefreshToken: "",
			ExpiresIn:    3600,
		},
	}
	svc := NewOpenAIOAuthService(nil, client)

	account := &Account{
		ID:       88,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "refresh-token-1",
		},
		Extra: map[string]any{
			"image_account_plan": "ChatGPT Free",
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "free", info.PlanType)
	require.Equal(t, int32(1), atomic.LoadInt32(&client.refreshCalls))

	credentials := svc.BuildAccountCredentials(info)
	require.Equal(t, "free", credentials["plan_type"])
}

func TestExtractPlanType_RecognizesAlternateOpenAIPlanFields(t *testing.T) {
	tests := []struct {
		name    string
		account map[string]any
		want    string
	}{
		{
			name: "top level account plan type",
			account: map[string]any{
				"account_plan_type": "ChatGPT Plus",
			},
			want: "plus",
		},
		{
			name: "nested account plan",
			account: map[string]any{
				"account": map[string]any{
					"account_plan": "ChatGPT Pro",
				},
			},
			want: "pro",
		},
		{
			name: "entitlement subscription plan",
			account: map[string]any{
				"entitlement": map[string]any{
					"subscription_plan": "team",
				},
			},
			want: "team",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, extractPlanType(testCase.account))
		})
	}
}
