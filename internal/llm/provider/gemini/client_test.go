package gemini

import (
	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/slack"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// Mock response structures for Gemini REST API
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

func TestGenerateContent_RetryLogic(t *testing.T) {
	// Setup mock server
	retryCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retryCount++

		var resp geminiResponse
		if retryCount <= 5 { // 5 retries means 6 attempts total to exceed, but here we test success on 3rd attempt
			// Return short response
			resp = geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Parts: []geminiPart{
								{Text: "あ"}, // 1 char
							},
							Role: "model",
						},
					},
				},
			}
		} else {
			// Return valid response
			resp = geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Parts: []geminiPart{
								{Text: "これは正常な長さの応答です。"}, // > 5 chars
							},
							Role: "model",
						},
					},
				},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		// Wrap in array as genai seems to expect it (possibly due to streaming endpoint or batch)
		json.NewEncoder(w).Encode([]geminiResponse{resp})
	}))
	defer ts.Close()

	// Configure client to use mock server
	ctx := context.Background()
	// NOTE: We assume genai.NewClient respects WithHTTPClient and WithEndpoint
	gClient, err := genai.NewClient(ctx, option.WithAPIKey("test-key"), option.WithEndpoint(ts.URL), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("Failed to create genai client: %v", err)
	}
	defer gClient.Close()

	cfg := &config.Config{
		GeminiModel: "gemini-1.5-pro",
	}

	client := &Client{
		client:      gClient,
		model:       gClient.GenerativeModel(cfg.GeminiModel),
		config:      cfg,
		slackClient: slack.NewClient("", "", "", ""), // Disabled client
	}

	// Test case: Success after retries

	messages := []model.Message{
		{Role: "user", Content: "こんにちは"},
	}

	respText, err := client.GenerateContent(ctx, messages, "", 100, nil, 0.0)
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}

	expected := "これは正常な長さの応答です。"
	if respText != expected {
		t.Errorf("Expected response %q, got %q", expected, respText)
	}

	if retryCount != 6 {
		t.Errorf("Expected 6 server calls, got %d", retryCount)
	}
}

func TestGenerateContent_MaxRetriesExceeded(t *testing.T) {
	// Setup mock server that always returns short response
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Parts: []geminiPart{
							{Text: "短い"}, // 2 chars
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]geminiResponse{resp})
	}))
	defer ts.Close()

	ctx := context.Background()
	gClient, err := genai.NewClient(ctx, option.WithAPIKey("test-key"), option.WithEndpoint(ts.URL), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("Failed to create genai client: %v", err)
	}
	defer gClient.Close()

	client := &Client{
		client:      gClient,
		model:       gClient.GenerativeModel("gemini-1.5-pro"),
		config:      &config.Config{},
		slackClient: slack.NewClient("", "", "", ""), // Disabled client
	}

	messages := []model.Message{
		{Role: "user", Content: "Hi"},
	}

	_, err = client.GenerateContent(ctx, messages, "", 100, nil, 0.0)
	if err == nil {
		t.Fatal("Expected error due to max retries exceeded, got nil")
	}

	expectedErr := "Gemini 生成応答が短すぎます (最大リトライ回数超過)"
	if err.Error() != expectedErr {
		t.Errorf("Expected error %q, got %q", expectedErr, err.Error())
	}
}
