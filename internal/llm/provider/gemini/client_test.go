package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
	Index        int           `json:"index,omitempty"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
}

func TestGenerateContentShortResponsePolicy(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(geminiResponse{
			Candidates: []geminiCandidate{{Content: geminiContent{Parts: []geminiPart{{Text: "短い"}}}}},
		})
	}))
	defer server.Close()

	ctx := t.Context()
	modelClient, err := genai.NewClient(ctx, option.WithAPIKey("test-key"), option.WithEndpoint(server.URL), option.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer modelClient.Close()
	client := &Client{
		model:  modelClient.GenerativeModel("gemini-test"),
		config: &config.Config{},
	}
	messages := []model.Message{{Role: model.RoleUser, Content: "Hi"}}

	got, _, err := client.GenerateContent(ctx, messages, "", 100, nil, 0)
	if err != nil || got != "短い" {
		t.Fatalf("ordinary response = %q, %v", got, err)
	}
	profileCtx := context.WithValue(ctx, model.ContextKeyIsProfileGeneration, true)
	_, _, err = client.GenerateContent(profileCtx, messages, "", 100, nil, 0)
	if !errors.Is(err, provider.ErrResponseTooShort) || requests.Load() != 2 {
		t.Fatalf("profile error = %v, requests = %d", err, requests.Load())
	}
}
