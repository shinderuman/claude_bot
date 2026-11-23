package slack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestPostMessageRateLimit(t *testing.T) {
	for _, test := range []struct {
		name       string
		retryAfter string
		calls      int32
	}{
		{name: "retry succeeds", retryAfter: "0", calls: 2},
		{name: "retry would exceed deadline", retryAfter: "60", calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", test.retryAfter)
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				_, _ = io.WriteString(w, `{"ok":true,"channel":"C12345","ts":"123.0"}`)
			}))
			defer server.Close()

			client := &Client{
				client:    slack.New("dummy-token", slack.OptionAPIURL(server.URL+"/")),
				channelID: "C12345",
				enabled:   true,
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := client.PostMessage(ctx, "test message")
			if test.calls == 1 {
				var limited *slack.RateLimitedError
				if !errors.As(err, &limited) {
					t.Fatalf("PostMessage error = %v, want a rate limit error", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := calls.Load(); got != test.calls {
				t.Fatalf("requests = %d, want %d", got, test.calls)
			}
		})
	}
}
