package llm

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func TestZAIErrorNotificationTitle(t *testing.T) {
	const zaiURL = "https://api.z.ai/api/anthropic"
	for _, tt := range []struct {
		name, baseURL, code, wantTitle string
		status                         int
	}{
		{name: "content restriction", baseURL: zaiURL, code: "1301", status: http.StatusBadRequest, wantTitle: "GLM 1301 入力または生成内容がコンテンツ制限に抵触"},
		{name: "temporary rate limit", baseURL: zaiURL, code: "1302", status: http.StatusTooManyRequests, wantTitle: "GLM 1302 リクエスト頻度の上限に到達"},
		{name: "five-hour quota", baseURL: zaiURL, code: "1320", status: http.StatusTooManyRequests, wantTitle: "GLM 1320 5時間枠超過"},
		{name: "unknown code", baseURL: zaiURL, code: "9999", status: http.StatusBadRequest, wantTitle: "LLM生成エラー"},
		{name: "no code", baseURL: zaiURL, status: http.StatusInternalServerError, wantTitle: "LLM生成エラー"},
		{name: "other endpoint", baseURL: "https://other.test", code: "1301", status: http.StatusBadRequest, wantTitle: "LLM生成エラー"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			type alert struct{ title, details string }
			alerts := make(chan alert, 1)
			previousNotifier := errorNotifier
			SetErrorNotifier(func(title, details string) { alerts <- alert{title, details} })
			t.Cleanup(func() { SetErrorNotifier(previousNotifier) })

			body := `{"type":"error","error":{"message":"request failed"}}`
			if tt.code != "" {
				body = `{"type":"error","error":{"code":"` + tt.code + `","message":"request failed"}}`
			}
			var payload []byte
			stubDefaultTransport(t, func(req *http.Request) (*http.Response, error) {
				var err error
				payload, err = io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				response := stubResponse(req, tt.status, "application/json", body)
				response.Header.Set("Request-Id", "request-123")
				return response, nil
			})
			client, err := NewClient(&config.Config{
				LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1,
				AnthropicBaseURL: tt.baseURL, AnthropicAuthToken: "test-token", AnthropicModel: "test-model",
			})
			if err != nil {
				t.Fatal(err)
			}
			const operation = "URL https://example.com/article のファクト抽出"
			var result []model.Fact
			err = client.GenerateJSON(t.Context(), []model.Message{{Role: model.RoleUser, Content: "page content"}},
				prompts.Messages.System.FactExtraction, 100, operation, &result)
			if err == nil || !strings.Contains(err.Error(), body) {
				t.Fatalf("error = %v, want original response %s", err, body)
			}
			wantDetails := fmt.Sprintf("%s失敗: POST %q: %d %s (Request-ID: request-123) %s",
				operation, tt.baseURL+"/v1/messages", tt.status, http.StatusText(tt.status), body)
			if tt.status == http.StatusBadRequest {
				wantDetails += "\n\n[Request Payload]\n" + string(payload)
			}
			select {
			case got := <-alerts:
				if got.title != tt.wantTitle || got.details != wantDetails {
					t.Fatalf("alert = %q, %q, want %q, %q", got.title, got.details, tt.wantTitle, wantDetails)
				}
			case <-time.After(time.Second):
				t.Fatal("error notification was not sent")
			}
		})
	}
}

func TestZAIRateLimitRetry(t *testing.T) {
	for _, tt := range []struct {
		name       string
		code       string
		retryAfter string
		wantCalls  int
		wantWait   time.Duration
	}{
		{name: "request limit", code: "1302", retryAfter: "2", wantCalls: 3, wantWait: 4 * time.Second},
		{name: "temporary overload", code: "1305", wantCalls: 3, wantWait: 3 * time.Second},
		{name: "wait exceeds retry budget", code: "1302", retryAfter: "61", wantCalls: 1},
		{name: "five-hour limit", code: "1316", retryAfter: "1", wantCalls: 1},
		{name: "five-hour limit with monthly cap", code: "1318", wantCalls: 1},
		{name: "five-hour limit with no extra usage", code: "1320", wantCalls: 1},
		{name: "usage quota", code: "1308", wantCalls: 1},
		{name: "unknown", code: "1999", wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			stubDefaultTransport(t, func(req *http.Request) (*http.Response, error) {
				calls++
				resp := stubResponse(req, http.StatusTooManyRequests, "application/json",
					`{"type":"error","error":{"code":"`+tt.code+`","message":"rate limited"}}`)
				resp.Header.Set("Retry-After", tt.retryAfter)
				return resp, nil
			})
			client, err := NewClient(&config.Config{
				LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1, LLMMaxRetries: 2,
				AnthropicBaseURL: "https://api.z.ai/api/anthropic", AnthropicAuthToken: "test-token", AnthropicModel: "glm-test",
			})
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			client.GenerateText(t.Context(), nil, "", 100, nil, 0, "テスト生成")
			if calls != tt.wantCalls || time.Since(start) < tt.wantWait {
				t.Fatalf("requests = %d, elapsed = %s, want %d requests and at least %s", calls, time.Since(start), tt.wantCalls, tt.wantWait)
			}
		})
	}
}

func TestZAITemporaryLimitDoesNotSuppressQuotaAlert(t *testing.T) {
	codes := []string{"1302", "1305", "1316", "1316"}
	var calls int
	stubDefaultTransport(t, func(req *http.Request) (*http.Response, error) {
		code := codes[calls]
		calls++
		return stubResponse(req, http.StatusTooManyRequests, "application/json",
			`{"type":"error","error":{"code":"`+code+`","message":"rate limited"}}`), nil
	})
	client, err := NewClient(&config.Config{
		LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1, RateLimitNotifyIntervalMinutes: 60,
		AnthropicBaseURL: "https://api.z.ai/api/anthropic", AnthropicAuthToken: "test-token", AnthropicModel: "glm-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, wantAlert := range []bool{true, false, true, false} {
		_, err := client.generate(t.Context(), nil, "", 100, nil, 0, "テスト生成", nil, nil)
		if err == nil {
			t.Fatalf("request with code %s succeeded", codes[i])
		}
		if alert := client.shouldNotifyRateLimit(err); alert != wantAlert {
			t.Fatalf("%s alert = %v, want %v", codes[i], alert, wantAlert)
		}
	}
}
