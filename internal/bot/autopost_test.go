package bot

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/facts"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

func TestAutoPostLogShowsPublishedText(t *testing.T) {
	const posted = "公開された本文です。"
	longParagraph := strings.Repeat("続", 45)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/messages":
			_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"生成リクエストの応答です。"}]}`)
		case "/api/v1/statuses":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"id": "123", "content": "<p>" + posted + "<br>" + longParagraph + "</p><p>#bot</p>",
			}); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1,
		AnthropicBaseURL: server.URL, AnthropicAuthToken: "test-token", AnthropicModel: "test-model",
		MaxPostChars: 500, AutoPostVisibility: "unlisted",
	}
	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	memory := store.NewMemoryFactStore()
	if err := memory.Add(t.Context(), model.Fact{Target: model.GeneralTarget, Key: "topic", Value: "fact"}); err != nil {
		t.Fatal(err)
	}
	factStore := store.NewFactStore(memory, "")
	b := &Bot{
		config: cfg, llmClient: llmClient, factStore: factStore,
		factService:    facts.NewFactService(cfg, factStore, llmClient, nil, nil, nil),
		mastodonClient: mastodon.NewClient(mastodon.Config{Server: server.URL, MaxPostChars: cfg.MaxPostChars}),
	}

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	b.executeAutoPost(t.Context())

	want := "自動投稿: " + string([]rune(posted + " " + longParagraph)[:LogContentMaxChars]) + "…"
	if !strings.HasSuffix(logs.String(), want+"\n") || strings.Count(logs.String(), "\n") != 1 {
		t.Fatalf("log output = %q, want one line ending with %q", logs.String(), want)
	}
}
