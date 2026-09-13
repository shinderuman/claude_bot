package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func TestExtractStructuredResult(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{{
			Name:  structuredResultToolName,
			Input: json.RawMessage(`{"result":{"intent":"chat"}}`),
		}},
	}

	got, err := extractStructuredResult(msg)
	if err != nil {
		t.Fatalf("extractStructuredResult() error = %v", err)
	}
	if got != `{"intent":"chat"}` {
		t.Fatalf("extractStructuredResult() = %s", got)
	}
}

func TestExtractStructuredResultRejectsMissingResult(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{{
			Name:  structuredResultToolName,
			Input: json.RawMessage(`{"result":null}`),
		}},
	}

	if _, err := extractStructuredResult(msg); err == nil {
		t.Fatal("extractStructuredResult() accepted null result")
	}
}

func TestGenerateStructuredContentFallsBackWhenToolsAreRejected(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		var request struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if len(request.Tools) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"tools unsupported"}}`))
			return
		}

		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","model":"test-model","content":[{"type":"text","text":"{\"target_candidates\":[],\"keys\":[]}"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	client := NewClient(&config.Config{
		AnthropicAuthToken: "test-token",
		AnthropicBaseURL:   server.URL,
		AnthropicModel:     "test-model",
	}).(*Client)

	got, _, err := client.GenerateStructuredContent(
		context.Background(),
		[]model.Message{{Role: model.RoleUser, Content: "query"}},
		"system",
		100,
		nil,
		0,
		querySchema(),
	)
	if err != nil {
		t.Fatalf("GenerateStructuredContent() error = %v", err)
	}
	if got != `{"target_candidates":[],"keys":[]}` {
		t.Fatalf("GenerateStructuredContent() = %s", got)
	}
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
}

func TestGenerateStructuredContentDoesNotFallbackOnUnrelatedBadRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"sensitive request"}}`))
	}))
	defer server.Close()

	client := NewClient(&config.Config{
		AnthropicAuthToken: "test-token",
		AnthropicBaseURL:   server.URL,
		AnthropicModel:     "test-model",
	}).(*Client)

	_, _, err := client.GenerateStructuredContent(
		context.Background(),
		[]model.Message{{Role: model.RoleUser, Content: "query"}},
		"system",
		100,
		nil,
		0,
		querySchema(),
	)
	if err == nil {
		t.Fatal("GenerateStructuredContent() accepted unrelated bad request")
	}
	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1", requestCount)
	}
}

func querySchema() *provider.StructuredSchema {
	return &provider.StructuredSchema{
		Type: "object",
		Properties: map[string]*provider.StructuredSchema{
			"target_candidates": {Type: "array", Items: &provider.StructuredSchema{Type: "string"}},
			"keys":              {Type: "array", Items: &provider.StructuredSchema{Type: "string"}},
		},
		Required: []string{"target_candidates", "keys"},
	}
}

func TestStructuredSchemaSerializesForAnthropic(t *testing.T) {
	schema := &provider.StructuredSchema{
		Type: "object",
		Properties: map[string]*provider.StructuredSchema{
			"value": {Type: "string"},
		},
		Required: []string{"value"},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}` {
		t.Fatalf("schema JSON = %s", data)
	}
}
