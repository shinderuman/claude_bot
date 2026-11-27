package openai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/siwc"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubHTTPClient(handler roundTripFunc) *http.Client {
	return &http.Client{Transport: handler}
}

func jsonHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sseHTTPResponse(req *http.Request, events string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(events)),
		Request:    req,
	}
}

func sseEvents(events ...string) string {
	return strings.Join(events, "\n\n") + "\n\n"
}

func requestForm(req *http.Request) url.Values {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		panic(err)
	}
	values, err := url.ParseQuery(string(data))
	if err != nil {
		panic(err)
	}
	return values
}

func writeCredential(t *testing.T, savedAt time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	credential := &siwc.Credential{
		Email:        "user@example.com",
		Subject:      "subject-1",
		ClientID:     "oaiapp_test",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Scopes:       strings.Fields(siwc.RequestScopes),
		SavedAt:      savedAt,
	}
	if err := siwc.SaveCredential(path, credential); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestClient(t *testing.T, handler roundTripFunc, credentialPath string) *Client {
	t.Helper()
	endpoints := siwc.Endpoints{AuthBaseURL: "https://auth.test", APIBaseURL: "https://api.test"}
	httpClient := stubHTTPClient(handler)
	return &Client{
		model:      "test-model",
		apiBaseURL: "https://api.test",
		session:    siwc.NewSession(credentialPath, endpoints, httpClient),
		httpClient: httpClient,
	}
}

func TestNewClientRequiresModelAndCredentialFile(t *testing.T) {
	credentialPath := writeCredential(t, time.Now().UTC())

	if _, err := NewClient(&config.Config{OpenAICredentialFile: credentialPath}); err == nil || !strings.Contains(err.Error(), "OPENAI_MODEL") {
		t.Fatalf("NewClient() without a model error = %v", err)
	}

	missing := filepath.Join(t.TempDir(), "absent.json")
	_, err := NewClient(&config.Config{OpenAIModel: "test-model", OpenAICredentialFile: missing})
	if err == nil || !strings.Contains(err.Error(), "openai_auth login") {
		t.Fatalf("NewClient() without a credential file error = %v", err)
	}
}

func TestGenerateContentSendsOfficialRequestShape(t *testing.T) {
	credentialPath := writeCredential(t, time.Now().UTC())
	var requestBody []byte
	var authorization string
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != siwc.ResponsesPath || req.Method != http.MethodPost {
			return jsonHTTPResponse(req, http.StatusNotFound, "{}"), nil
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
		}
		requestBody = body
		authorization = req.Header.Get("Authorization")
		return sseHTTPResponse(req, sseEvents(
			`data: {"type":"response.created","response":{}}`,
			`data: {"type":"response.output_text.delta","delta":"こんにちは"}`,
			`data: {"type":"response.output_text.delta","delta":"、世界"}`,
			`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
		)), nil
	}, credentialPath)

	messages := []model.Message{
		{Role: model.RoleUser, Content: "こんにちは"},
		{Role: model.RoleAssistant, Content: "どうしました"},
		{Role: model.RoleUser, Content: "調子はどう？"},
	}
	content, payload, err := client.GenerateContent(t.Context(), messages, "システムプロンプト", 100, nil, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if content != "こんにちは、世界" {
		t.Fatalf("content = %q", content)
	}
	if payload != "" {
		t.Fatal("OpenAI returned the request body for logging")
	}
	if authorization != "Bearer access-1" {
		t.Fatalf("authorization = %q", authorization)
	}

	var request map[string]any
	if err := json.Unmarshal(requestBody, &request); err != nil {
		t.Fatal(err)
	}
	if request["model"] != "test-model" {
		t.Errorf("model = %v", request["model"])
	}
	if request["instructions"] != "システムプロンプト" {
		t.Errorf("instructions = %v", request["instructions"])
	}
	if request["store"] != false {
		t.Errorf("store = %v, want false", request["store"])
	}
	if request["stream"] != true {
		t.Errorf("stream = %v, want true", request["stream"])
	}
	for _, unsupported := range []string{"temperature", "max_output_tokens", "top_p", "user", "metadata", "prompt", "truncation"} {
		if _, present := request[unsupported]; present {
			t.Errorf("request contains unsupported field %s", unsupported)
		}
	}

	input, ok := request["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("input = %#v", request["input"])
	}
	first, _ := input[0].(map[string]any)
	if first["role"] != model.RoleUser || first["content"] != "こんにちは" {
		t.Errorf("first input item = %#v", first)
	}
	second, _ := input[1].(map[string]any)
	if second["role"] != model.RoleAssistant || second["content"] != "どうしました" {
		t.Errorf("second input item = %#v", second)
	}
}

func TestGenerateContentSendsImagesOnLastUserMessage(t *testing.T) {
	credentialPath := writeCredential(t, time.Now().UTC())
	var request map[string]any
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		return sseHTTPResponse(req, sseEvents(`data: {"type":"response.completed","response":{}}`)), nil
	}, credentialPath)

	images := []model.Image{{Data: "aW1hZ2U=", MediaType: "image/png"}}
	_, _, err := client.GenerateContent(t.Context(), []model.Message{{Role: model.RoleUser, Content: "この画像は何ですか"}}, "", 100, images, 0)
	if err != nil {
		t.Fatal(err)
	}

	input, _ := request["input"].([]any)
	last, _ := input[len(input)-1].(map[string]any)
	if last["role"] != model.RoleUser {
		t.Fatalf("last message role = %v", last["role"])
	}
	parts, ok := last["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("last message content = %#v", last["content"])
	}
	textPart, _ := parts[0].(map[string]any)
	if textPart["type"] != "input_text" || textPart["text"] != "この画像は何ですか" {
		t.Errorf("text part = %#v", textPart)
	}
	imagePart, _ := parts[1].(map[string]any)
	if imagePart["type"] != "input_image" || imagePart["image_url"] != "data:image/png;base64,aW1hZ2U=" {
		t.Errorf("image part = %#v", imagePart)
	}
}

func TestGenerateContentRefreshesExpiredTokenBeforeRequest(t *testing.T) {
	credentialPath := writeCredential(t, time.Now().UTC().Add(-2*time.Hour))
	var refreshForm url.Values
	var authorization string
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case siwc.TokenPath:
			refreshForm = requestForm(req)
			return jsonHTTPResponse(req, 200, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600,"scope":"`+siwc.RequestScopes+`"}`), nil
		case siwc.ResponsesPath:
			authorization = req.Header.Get("Authorization")
			return sseHTTPResponse(req, sseEvents(`data: {"type":"response.completed","response":{}}`)), nil
		}
		return jsonHTTPResponse(req, http.StatusNotFound, "{}"), nil
	}, credentialPath)

	_, _, err := client.GenerateContent(t.Context(), []model.Message{{Role: model.RoleUser, Content: "hi"}}, "", 100, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if refreshForm.Get("grant_type") != "refresh_token" || refreshForm.Get("refresh_token") != "refresh-1" {
		t.Fatalf("refresh form = %v", refreshForm)
	}
	if authorization != "Bearer access-2" {
		t.Fatalf("authorization = %q, want the refreshed token", authorization)
	}

	saved, err := siwc.LoadCredential(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "access-2" || saved.RefreshToken != "refresh-2" {
		t.Fatalf("credential was not rotated: %+v", saved)
	}
}

func TestGenerateContentStreamFailures(t *testing.T) {
	credentialPath := writeCredential(t, time.Now().UTC())
	tests := []struct {
		name            string
		events          string
		readErr         error
		wantErr         error
		wantDiagnostic  string
		wantRateLimited bool
		wantRetryable   bool
	}{
		{
			name:    "unterminated stream",
			events:  sseEvents(`data: {"type":"response.output_text.delta","delta":"途中まで"}`),
			wantErr: errStreamUnterminated,
		},
		{
			name:           "legacy sentinel before completion",
			events:         sseEvents("data: [DONE]", `data: {"type":"response.completed"}`),
			wantDiagnostic: "応答ストリームのイベントを解析できません",
		},
		{
			name:    "completion event cut off before blank line",
			events:  "data: {\"type\":\"response.completed\"}\n",
			wantErr: errStreamUnterminated,
		},
		{
			name:           "incomplete response",
			events:         sseEvents(`data: {"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`),
			wantErr:        errStreamIncomplete,
			wantDiagnostic: "max_output_tokens",
		},
		{
			name:    "interrupted body",
			events:  sseEvents(`data: {"type":"response.output_text.delta","delta":"途中まで"}`),
			readErr: io.ErrUnexpectedEOF,
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:            "usage limit exceeded",
			events:          sseEvents(`data: {"type":"response.failed","response":{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit exceeded"}}}`),
			wantRateLimited: true,
		},
		{
			name:          "usage unavailable",
			events:        sseEvents(`data: {"type":"response.failed","response":{"error":{"code":"subscription_sharing_usage_unavailable","message":"temporarily unavailable"}}}`),
			wantRetryable: true,
		},
		{
			name:   "user not eligible",
			events: sseEvents(`data: {"type":"response.failed","response":{"error":{"code":"subscription_sharing_user_not_eligible","message":"not eligible"}}}`),
		},
		{
			name:   "top level error event",
			events: sseEvents(`data: {"type":"error","code":"subscription_sharing_route_not_supported","message":"route not supported"}`),
		},
		{
			name:           "unknown failure code",
			events:         sseEvents(`data: {"type":"response.failed","response":{"error":{"code":"server_error","message":"upstream failure"}}}`),
			wantDiagnostic: "response.failed server_error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
				resp := sseHTTPResponse(req, test.events)
				resp.Header.Set("x-request-id", "req-stream")
				if test.readErr != nil {
					resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(test.events), iotest.ErrReader(test.readErr)))
				}
				return resp, nil
			}, credentialPath)

			_, _, err := client.GenerateContent(t.Context(), []model.Message{{Role: model.RoleUser, Content: "hi"}}, "", 100, nil, 0)
			if err == nil {
				t.Fatal("GenerateContent() succeeded for a failed stream")
			}
			var apiErr *ResponsesError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusOK || apiErr.RequestID != "req-stream" {
				t.Fatalf("error = %v, want original HTTP status and request ID", err)
			}
			if message := err.Error(); !strings.Contains(message, "HTTP 200") || !strings.Contains(message, "req-stream") || !strings.Contains(message, test.wantDiagnostic) || strings.Contains(message, "status 0") {
				t.Errorf("incomplete stream diagnostics: %q", message)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if client.IsRateLimited(err) != test.wantRateLimited {
				t.Errorf("IsRateLimited() = %v, want %v", client.IsRateLimited(err), test.wantRateLimited)
			}
			if client.IsRetryable(err) != test.wantRetryable {
				t.Errorf("IsRetryable() = %v, want %v", client.IsRetryable(err), test.wantRetryable)
			}
			if client.IsBadRequest(err) {
				t.Error("IsBadRequest() = true for a stream failure")
			}
		})
	}
}

func TestGenerateContentHTTPFailures(t *testing.T) {
	credentialPath := writeCredential(t, time.Now().UTC())
	tests := []struct {
		name            string
		status          int
		body            string
		wantCode        string
		wantParam       string
		wantRequestID   string
		wantRateLimited bool
		wantRetryable   bool
		wantBadRequest  bool
		wantReauthHint  bool
	}{
		{
			name:           "unsupported capability",
			status:         http.StatusBadRequest,
			body:           `{"error":{"code":"subscription_sharing_unsupported_capability","message":"field is unsupported","param":"temperature"}}`,
			wantCode:       "subscription_sharing_unsupported_capability",
			wantParam:      "temperature",
			wantRequestID:  "req-400",
			wantBadRequest: true,
		},
		{
			name:            "usage limit exceeded",
			status:          http.StatusTooManyRequests,
			body:            `{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit"}}`,
			wantCode:        "subscription_sharing_usage_limit_exceeded",
			wantRateLimited: true,
		},
		{
			name:           "direct admission rejected",
			status:         http.StatusUnauthorized,
			body:           `{"detail":"The required signed identity or direct permission was not accepted."}`,
			wantRequestID:  "req-401",
			wantReauthHint: true,
		},
		{
			name:          "routing unavailable",
			status:        http.StatusServiceUnavailable,
			body:          `{"detail":"Direct routing is unavailable."}`,
			wantRequestID: "req-503",
			wantRetryable: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
				response := jsonHTTPResponse(req, test.status, test.body)
				response.Header.Set("x-request-id", test.wantRequestID)
				return response, nil
			}, credentialPath)

			_, payload, err := client.GenerateContent(t.Context(), []model.Message{{Role: model.RoleUser, Content: "hi"}}, "", 100, nil, 0)
			if err == nil {
				t.Fatal("GenerateContent() succeeded for an HTTP failure")
			}
			if payload != "" {
				t.Fatal("OpenAI returned the request body for logging")
			}
			var apiErr *ResponsesError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want a ResponsesError", err)
			}
			if apiErr.Code != test.wantCode {
				t.Errorf("code = %q, want %q", apiErr.Code, test.wantCode)
			}
			if apiErr.Param != test.wantParam {
				t.Errorf("param = %q, want %q", apiErr.Param, test.wantParam)
			}
			if test.wantRequestID != "" && apiErr.RequestID != test.wantRequestID {
				t.Errorf("request id = %q, want %q", apiErr.RequestID, test.wantRequestID)
			}
			if client.IsRateLimited(err) != test.wantRateLimited {
				t.Errorf("IsRateLimited() = %v, want %v", client.IsRateLimited(err), test.wantRateLimited)
			}
			if client.IsRetryable(err) != test.wantRetryable {
				t.Errorf("IsRetryable() = %v, want %v", client.IsRetryable(err), test.wantRetryable)
			}
			if client.IsBadRequest(err) != test.wantBadRequest {
				t.Errorf("IsBadRequest() = %v, want %v", client.IsBadRequest(err), test.wantBadRequest)
			}
			if hint := strings.Contains(err.Error(), "openai_auth login"); hint != test.wantReauthHint {
				t.Errorf("reauth hint = %v, want %v", hint, test.wantReauthHint)
			}
			if test.wantRequestID != "" && !strings.Contains(err.Error(), test.wantRequestID) {
				t.Errorf("error message = %q, want it to keep the request id", err.Error())
			}
		})
	}
}

func TestStructuredOutputIsNotUsed(t *testing.T) {
	credentialPath := writeCredential(t, time.Now().UTC())
	client := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Error("structured requests must not reach the API")
		return nil, nil
	}, credentialPath)

	if client.SupportsStructuredOutput() {
		t.Fatal("SupportsStructuredOutput() = true")
	}
	if _, _, err := client.GenerateStructuredContent(t.Context(), nil, "", 0, nil, 0, nil); err == nil {
		t.Fatal("GenerateStructuredContent() must fail closed")
	}
}

func TestConsumeResponseStreamIgnoresUnknownJSONEvents(t *testing.T) {
	events := sseEvents(
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{}}",
		`data: {"type":"response.output_item.added","output_index":0}`,
		`data: {"type":"response.output_text.delta","delta":"本文"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
	)
	content, err := consumeResponseStream(strings.NewReader(events))
	if err != nil {
		t.Fatal(err)
	}
	if content != "本文" {
		t.Fatalf("content = %q", content)
	}
}

func TestConsumeResponseStreamRequiresFinalBlankLine(t *testing.T) {
	const event = `data: {"type":"response.completed"}`
	for _, eol := range []string{"\n", "\r\n", "\r"} {
		for _, suffix := range []string{"", eol} {
			content, err := consumeResponseStream(strings.NewReader(event + suffix))
			if content != "" || err != errStreamUnterminated {
				t.Fatalf("stream ending %q returned %q, %v; want an unterminated stream error", suffix, content, err)
			}
		}
		content, err := consumeResponseStream(strings.NewReader("\ufeff" + event + eol + eol))
		if content != "" || err != nil {
			t.Fatalf("stream with terminator %q returned %q, %v", eol+eol, content, err)
		}
	}
}

func TestCredentialFilePermissionsAreOwnerOnly(t *testing.T) {
	credentialPath := writeCredential(t, time.Now().UTC())
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential file mode = %v, want 0600", info.Mode().Perm())
	}
}
