package slack

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// Mock RateLimitedError
type mockRateLimitedError struct{}

func (e *mockRateLimitedError) Error() string             { return "too many requests" }
func (e *mockRateLimitedError) Retryable() bool           { return true }
func (e *mockRateLimitedError) RetryAfter() time.Duration { return 1 * time.Second }

func TestIsRateLimitedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "Nil error",
			err:  nil,
			want: false,
		},
		{
			name: "Generic error",
			err:  errors.New("some error"),
			want: false,
		},
		{
			name: "Slack RateLimitedError",
			err:  &slack.RateLimitedError{RetryAfter: 1 * time.Second},
			want: true,
		},
		{
			name: "Custom mock RateLimitedError",
			err:  &mockRateLimitedError{},
			want: true,
		},
		{
			name: "Error string containing 'rate limited'",
			err:  errors.New("running in rate limited mode"),
			want: true,
		},
		{
			name: "Error string containing 'too many requests'",
			err:  errors.New("too many requests"),
			want: true,
		},
		{
			name: "Error string containing '429'",
			err:  errors.New("server returned 429"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRateLimitedError(tt.err); got != tt.want {
				t.Errorf("isRateLimitedError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Note: TestPostMessageToChannel_Backoff is hard to implement with pure unit tests
// without mocking the internal slack.Client or abstracting time/sleep.
// For now, we rely on the logic review and isRateLimitedError test.

// TestPostMessageToChannel_RetryLogic verifies the backoff loop using a mock server
func TestPostMessageToChannel_RetryLogic(t *testing.T) {
	// Setup: Temporary override of time variables for fast testing
	origInitialBackoff := initialBackoff
	origMaxBackoffDelay := maxBackoffDelay
	origMaxTotalTimeout := maxTotalTimeout
	defer func() {
		initialBackoff = origInitialBackoff
		maxBackoffDelay = origMaxBackoffDelay
		maxTotalTimeout = origMaxTotalTimeout
	}()

	// Set very short durations
	initialBackoff = 1 * time.Millisecond
	maxBackoffDelay = 5 * time.Millisecond
	maxTotalTimeout = 100 * time.Millisecond // Should timeout after a few retries

	// 1. Start a mock server that always returns 429
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// slack-go expects a JSON response usually, but for 429 just status might be enough,
		// but let's send a standard body just in case it tries to parse error.
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		if _, err := w.Write([]byte(`{"ok":false, "error":"ratelimited"}`)); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	// 2. Create a Slack client pointing to the mock server
	// We use the Slack option to change the API URL base
	sClient := slack.New("dummy-token", slack.OptionAPIURL(server.URL+"/"))

	client := &Client{
		client:    sClient,
		channelID: "C12345",
		enabled:   true,
	}

	// 3. Execute PostMessageToChannel
	// It should retry until maxTotalTimeout (100ms)
	ctx := context.Background()
	err := client.PostMessageToChannel(ctx, "C12345", "test message")

	// 4. Assertions
	if err != nil {
		t.Errorf("Expected nil error (silent failure on timeout), got %v", err)
	}

	// We expect multiple calls due to retries
	if callCount < 2 {
		t.Errorf("Expected retries, got callCount=%d", callCount)
	}
}
