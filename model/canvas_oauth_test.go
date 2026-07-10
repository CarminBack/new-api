package model

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConsumeCanvasOAuthCodeRequiresMatchingPKCEAndIsSingleUse(t *testing.T) {
	truncateTables(t)
	now := time.Now().Unix()
	code := &CanvasOAuthCode{
		CodeHash:      "hash-single-use",
		UserId:        42,
		ClientId:      "canvas",
		RedirectUri:   "https://canvas.example/auth/callback",
		CodeChallenge: "challenge-correct",
		CreatedAt:     now,
		ExpiresAt:     now + 60,
	}
	require.NoError(t, CreateCanvasOAuthCode(code))

	_, err := ConsumeCanvasOAuthCode(code.CodeHash, code.ClientId, code.RedirectUri, "challenge-wrong", now)
	require.ErrorIs(t, err, ErrCanvasOAuthCodeInvalid)

	consumed, err := ConsumeCanvasOAuthCode(code.CodeHash, code.ClientId, code.RedirectUri, code.CodeChallenge, now)
	require.NoError(t, err)
	require.Equal(t, code.UserId, consumed.UserId)
	require.Equal(t, now, consumed.UsedAt)

	_, err = ConsumeCanvasOAuthCode(code.CodeHash, code.ClientId, code.RedirectUri, code.CodeChallenge, now)
	require.ErrorIs(t, err, ErrCanvasOAuthCodeInvalid)
}

func TestConsumeCanvasOAuthCodeRejectsExpiredCode(t *testing.T) {
	truncateTables(t)
	now := time.Now().Unix()
	code := &CanvasOAuthCode{
		CodeHash:      "hash-expired",
		UserId:        42,
		ClientId:      "canvas",
		RedirectUri:   "https://canvas.example/auth/callback",
		CodeChallenge: "challenge",
		CreatedAt:     now - 120,
		ExpiresAt:     now - 1,
	}
	require.NoError(t, DB.Create(code).Error)

	_, err := ConsumeCanvasOAuthCode(code.CodeHash, code.ClientId, code.RedirectUri, code.CodeChallenge, now)
	require.ErrorIs(t, err, ErrCanvasOAuthCodeInvalid)
}

func TestConsumeCanvasOAuthCodeConcurrentUseHasOneWinner(t *testing.T) {
	truncateTables(t)
	now := time.Now().Unix()
	code := &CanvasOAuthCode{
		CodeHash:      "hash-concurrent",
		UserId:        42,
		ClientId:      "canvas",
		RedirectUri:   "https://canvas.example/auth/callback",
		CodeChallenge: "challenge",
		CreatedAt:     now,
		ExpiresAt:     now + 60,
	}
	require.NoError(t, CreateCanvasOAuthCode(code))

	const attempts = 4
	start := make(chan struct{})
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := ConsumeCanvasOAuthCode(code.CodeHash, code.ClientId, code.RedirectUri, code.CodeChallenge, now)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	invalid := 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrCanvasOAuthCodeInvalid) {
			invalid++
		} else {
			t.Fatalf("unexpected consume error: %v", err)
		}
	}
	require.Equal(t, 1, successes, fmt.Sprintf("expected one successful consumer, got %d", successes))
	require.Equal(t, attempts-1, invalid)
}
