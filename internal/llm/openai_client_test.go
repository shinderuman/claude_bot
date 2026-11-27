package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
	"claude_bot/internal/siwc"
)

const responsesURL = siwc.OfficialAPIBaseURL + siwc.ResponsesPath

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubDefaultTransport(t *testing.T, handler roundTripFunc) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = handler
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func stubResponse(req *http.Request, status int, contentType string, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": {contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sseStream(events ...string) string {
	var builder strings.Builder
	for _, event := range events {
		builder.WriteString("data: " + event + "\n\n")
	}
	return builder.String()
}

func completedStream(text string) string {
	delta, err := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": text})
	if err != nil {
		panic(err)
	}
	return sseStream(string(delta), `{"type":"response.completed","response":{}}`)
}

func failedStream(code string) string {
	return sseStream(`{"type":"response.failed","response":{"error":{"code":"` + code + `","message":"plan usage stopped"}}}`)
}

func openAICredentialPath(t *testing.T, savedAt time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := siwc.SaveCredential(path, &siwc.Credential{
		Email:        "user@example.com",
		Subject:      "subject-1",
		ClientID:     "oaiapp_test",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Scopes:       strings.Fields(siwc.RequestScopes),
		SavedAt:      savedAt.UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func openAIClient(t *testing.T, credentialPath string, maxRetries int, handler roundTripFunc) *Client {
	t.Helper()
	stubDefaultTransport(t, handler)
	client, err := NewClient(&config.Config{
		LLMProvider:          config.LLMProviderOpenAI,
		LLMMaxConcurrency:    1,
		LLMMaxRetries:        maxRetries,
		OpenAICredentialFile: credentialPath,
		OpenAIModel:          "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func openAIFactQuery(client *Client) string {
	return generateJSONForTest(context.Background(), client, prompts.Messages.System.FactQuery)
}

func TestGenerateTextWithOpenAIProviderUsesPromptSchemaValidation(t *testing.T) {
	const result = `{"target_candidates":["user"],"keys":[]}`
	var requests int
	var body []byte
	var authorization string
	client := openAIClient(t, openAICredentialPath(t, time.Now()), 0, func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != responsesURL || req.Method != http.MethodPost {
			t.Errorf("request = %s %s", req.Method, req.URL)
		}
		authorization = req.Header.Get("Authorization")
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
		}
		body = data
		return stubResponse(req, http.StatusOK, "text/event-stream", sseStream(
			`{"type":"response.output_text.delta","delta":"{\"target_candidates\":[\"user\"],"}`,
			`{"type":"response.output_text.delta","delta":"\"keys\":[]}"}`,
			`{"type":"response.completed","response":{}}`,
		)), nil
	})

	if got := openAIFactQuery(client); got != result {
		t.Fatalf("GenerateText() = %q, want %q", got, result)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if authorization != "Bearer access-1" {
		t.Fatalf("authorization = %q", authorization)
	}

	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if request["model"] != "test-model" || request["instructions"] != prompts.Messages.System.FactQuery {
		t.Errorf("model/instructions = %v / %v", request["model"], request["instructions"])
	}
	if request["stream"] != true || request["store"] != false {
		t.Errorf("stream = %v, store = %v; want streaming without storage", request["stream"], request["store"])
	}
	for _, unsupported := range []string{"temperature", "max_output_tokens", "top_p", "tools", "previous_response_id", "user"} {
		if _, present := request[unsupported]; present {
			t.Errorf("request contains unsupported field %s", unsupported)
		}
	}
	input, ok := request["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v", request["input"])
	}
	first, _ := input[0].(map[string]any)
	if first["role"] != model.RoleUser || first["content"] != "query" {
		t.Errorf("first input item = %#v", first)
	}
}

func TestGenerateTextWithOpenAIProviderStopsWithoutRetryingPlanFailures(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{
			name:        "invalid structured output",
			status:      http.StatusOK,
			contentType: "text/event-stream",
			body:        completedStream("これはJSONではありません"),
		},
		{
			name:        "usage limit exceeded",
			status:      http.StatusOK,
			contentType: "text/event-stream",
			body:        failedStream("subscription_sharing_usage_limit_exceeded"),
		},
		{
			name:        "user not eligible",
			status:      http.StatusOK,
			contentType: "text/event-stream",
			body:        failedStream("subscription_sharing_user_not_eligible"),
		},
		{
			name:        "unsupported capability",
			status:      http.StatusBadRequest,
			contentType: "application/json",
			body:        `{"error":{"code":"subscription_sharing_unsupported_capability","message":"field is unsupported","param":"temperature"}}`,
		},
		{
			name:        "unterminated stream",
			status:      http.StatusOK,
			contentType: "text/event-stream",
			body:        sseStream(`{"type":"response.output_text.delta","delta":"途中まで"}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests int
			client := openAIClient(t, openAICredentialPath(t, time.Now()), 5, func(req *http.Request) (*http.Response, error) {
				requests++
				return stubResponse(req, test.status, test.contentType, test.body), nil
			})

			if got := openAIFactQuery(client); got != "" {
				t.Fatalf("GenerateText() = %q, want empty", got)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1 without a retry loop", requests)
			}
		})
	}
}

func TestOpenAI400LogsDiagnosticsWithoutRequestContent(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	notifications := make(chan string, 1)
	notifier := errorNotifier
	SetErrorNotifier(func(_, details string) { notifications <- details })
	t.Cleanup(func() { SetErrorNotifier(notifier) })

	client := openAIClient(t, openAICredentialPath(t, time.Now()), 3, func(req *http.Request) (*http.Response, error) {
		resp := stubResponse(req, http.StatusBadRequest, "application/json",
			`{"error":{"code":"subscription_sharing_unsupported_capability","param":"input[0].content[1]","message":"画像入力は利用できません"}}`)
		resp.Header.Set("x-request-id", "req-400")
		return resp, nil
	})
	images := []model.Image{{MediaType: "image/png", Data: "cHJpdmF0ZS1pbWFnZQ=="}}
	got := client.GenerateText(t.Context(), []model.Message{{Role: model.RoleUser, Content: "private user text"}},
		"private system prompt", 100, images, 0, "返信生成")
	if got != "" {
		t.Fatalf("GenerateText() = %q, want failure", got)
	}

	var details string
	select {
	case details = <-notifications:
	case <-time.After(time.Second):
		t.Fatal("OpenAI error notification was not sent")
	}
	for _, output := range []string{logs.String(), details} {
		for _, want := range []string{"400", "subscription_sharing_unsupported_capability", "input[0].content[1]", "req-400"} {
			if !strings.Contains(output, want) {
				t.Errorf("missing diagnostic %q in %q", want, output)
			}
		}
		for _, private := range []string{"private user text", "private system prompt", images[0].Data, "LLMリクエスト", "[Request Payload]"} {
			if strings.Contains(output, private) {
				t.Errorf("request content was included in the error output: %q", private)
			}
		}
	}
}

func TestOpenAIProviderUsesOfficialHostAndRejectsRedirect(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://other.invalid")
	t.Setenv("OPENAI_AUTH_BASE_URL", "https://other.invalid")
	path := openAICredentialPath(t, time.Now())
	requests := 0
	client := openAIClient(t, path, 0, func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != responsesURL {
			t.Errorf("request URL = %s, want %s", req.URL, responsesURL)
		}
		resp := stubResponse(req, http.StatusTemporaryRedirect, "application/json", "{}")
		resp.Header.Set("Location", "https://other.invalid")
		return resp, nil
	})

	result := client.GenerateText(t.Context(), []model.Message{{Role: model.RoleUser, Content: "query"}}, "", 100, nil, 0, "返信生成")
	if result != "" || requests != 1 {
		t.Fatalf("result = %q, requests = %d, want one rejected request", result, requests)
	}
}

func TestGenerateTextWithOpenAIProviderRetriesUnavailableRouting(t *testing.T) {
	var requests int
	client := openAIClient(t, openAICredentialPath(t, time.Now()), 1, func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return stubResponse(req, http.StatusServiceUnavailable, "application/json", `{"detail":"Direct routing is unavailable."}`), nil
		}
		return stubResponse(req, http.StatusOK, "text/event-stream", completedStream("復旧後の応答")), nil
	})

	got := client.GenerateText(context.Background(), []model.Message{{Role: model.RoleUser, Content: "query"}},
		prompts.Messages.System.Base, 100, nil, 0.5, "返信生成")
	if got != "復旧後の応答" {
		t.Fatalf("GenerateText() = %q", got)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestGenerateTextWithOpenAIProviderResumesAfterReauthentication(t *testing.T) {
	credentialPath := openAICredentialPath(t, time.Now().Add(-2*time.Hour))
	var tokenRequests, responseRequests int
	client := openAIClient(t, credentialPath, 5, func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case siwc.OfficialEndpoints().TokenURL():
			tokenRequests++
			return stubResponse(req, http.StatusBadRequest, "application/json", `{"error":"refresh_token_reused","error_description":"refresh token was already used"}`), nil
		case responsesURL:
			responseRequests++
			if req.Header.Get("Authorization") != "Bearer access-2" {
				t.Errorf("authorization = %q", req.Header.Get("Authorization"))
			}
			return stubResponse(req, http.StatusOK, "text/event-stream", completedStream(`{"target_candidates":[],"keys":[]}`)), nil
		}
		return stubResponse(req, http.StatusNotFound, "application/json", `{}`), nil
	})

	if got := openAIFactQuery(client); got != "" {
		t.Fatalf("GenerateText() = %q before reauthentication, want empty", got)
	}
	if tokenRequests != 1 || responseRequests != 0 {
		t.Fatalf("requests = token:%d responses:%d, want the failed refresh to stop inference", tokenRequests, responseRequests)
	}
	cleared, err := siwc.LoadCredential(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.RefreshToken != "" || cleared.ClientID != "oaiapp_test" {
		t.Fatalf("credential after an unusable refresh = %+v, want cleared tokens and a kept registration", cleared)
	}

	reauthenticated := &siwc.Credential{
		Email:        cleared.Email,
		Subject:      cleared.Subject,
		ClientID:     cleared.ClientID,
		AccessToken:  "access-2",
		RefreshToken: "refresh-2",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Scopes:       strings.Fields(siwc.RequestScopes),
		SavedAt:      time.Now().UTC(),
	}
	if err := siwc.SaveCredential(credentialPath, reauthenticated); err != nil {
		t.Fatal(err)
	}

	if got := openAIFactQuery(client); got != `{"target_candidates":[],"keys":[]}` {
		t.Fatalf("GenerateText() = %q after reauthentication", got)
	}
	if tokenRequests != 1 || responseRequests != 1 {
		t.Fatalf("requests = token:%d responses:%d, want inference to resume on the existing client", tokenRequests, responseRequests)
	}
}
