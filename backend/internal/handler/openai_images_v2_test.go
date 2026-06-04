package handler

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/service/openaiimages"
)

type imageAccountRepoStub struct {
	service.AccountRepository

	groupAccounts []service.Account
	groupID       int64
	schedGroupHit bool
}

func (s *imageAccountRepoStub) ListByGroup(_ context.Context, groupID int64) ([]service.Account, error) {
	s.groupID = groupID
	return s.groupAccounts, nil
}

func (s *imageAccountRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, _ string) ([]service.Account, error) {
	s.schedGroupHit = true
	return nil, nil
}

func TestListOpenAIImageAccountsKeepsTextRateLimitedGroupedOAuth(t *testing.T) {
	now := time.Now()
	textRateLimitReset := now.Add(time.Hour)
	overloadedUntil := now.Add(time.Hour)
	tempUnschedulableUntil := now.Add(time.Hour)
	expiredAt := now.Add(-time.Minute)

	repo := &imageAccountRepoStub{
		groupAccounts: []service.Account{
			{
				ID:               42,
				Platform:         service.PlatformOpenAI,
				Type:             service.AccountTypeOAuth,
				Status:           service.StatusActive,
				Schedulable:      true,
				RateLimitResetAt: &textRateLimitReset,
				Credentials: map[string]any{
					"access_token": "web-token",
				},
			},
			{
				ID:          43,
				Platform:    service.PlatformAnthropic,
				Type:        service.AccountTypeOAuth,
				Status:      service.StatusActive,
				Schedulable: true,
			},
			{
				ID:            44,
				Platform:      service.PlatformOpenAI,
				Type:          service.AccountTypeOAuth,
				Status:        service.StatusActive,
				Schedulable:   true,
				OverloadUntil: &overloadedUntil,
			},
			{
				ID:                      45,
				Platform:                service.PlatformOpenAI,
				Type:                    service.AccountTypeOAuth,
				Status:                  service.StatusActive,
				Schedulable:             true,
				TempUnschedulableUntil:  &tempUnschedulableUntil,
				TempUnschedulableReason: "retryable upstream error",
			},
			{
				ID:                 46,
				Platform:           service.PlatformOpenAI,
				Type:               service.AccountTypeOAuth,
				Status:             service.StatusActive,
				Schedulable:        true,
				AutoPauseOnExpired: true,
				ExpiresAt:          &expiredAt,
			},
			{
				ID:          47,
				Platform:    service.PlatformOpenAI,
				Type:        service.AccountTypeOAuth,
				Status:      service.StatusActive,
				Schedulable: false,
			},
		},
	}

	accounts, err := listOpenAIImageAccounts(context.Background(), repo, openaiimages.PoolFilter{
		GroupID: 99,
		Driver:  openaiimages.DriverWeb,
	})
	if err != nil {
		t.Fatalf("listOpenAIImageAccounts returned error: %v", err)
	}
	if repo.schedGroupHit {
		t.Fatal("image account listing must not use text schedulable group query")
	}
	if repo.groupID != 99 {
		t.Fatalf("groupID=%d want 99", repo.groupID)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts len=%d want 1: %#v", len(accounts), accounts)
	}
	if accounts[0].ID != 42 {
		t.Fatalf("account id=%d want 42", accounts[0].ID)
	}
	if accounts[0].AccessToken != "web-token" {
		t.Fatalf("access token=%q want web-token", accounts[0].AccessToken)
	}
}
