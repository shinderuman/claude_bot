package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"claude_bot/internal/config"
	chat "claude_bot/internal/conversation"
	"claude_bot/internal/facts"
	"claude_bot/internal/fetcher"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"

	gomastodon "github.com/mattn/go-mastodon"
)

func TestAnalysisURLCandidatesUseFullAuthoredLinks(t *testing.T) {
	const start = "https://example.org/@user/123?ref=post&amp;via=web"
	const end = "https://example.org/@user/456"
	content := `<p><a href="https://example.org/@bot" class="mention">@bot</a> この範囲をまとめて
		<a href="` + start + `"><span>https://example.org/@user/12</span><span>…</span></a>
		` + end + ` <a href="` + end + `">終了</a><a href="https://example.org/articles/789">参考記事</a></p>`

	got := analysisURLCandidates(fetcher.ExtractPostURLs(content))
	want := []string{"https://example.org/@user/123?ref=post&via=web", end}
	if !slices.Equal(got, want) {
		t.Fatalf("analysisURLCandidates() = %q, want %q", got, want)
	}
	if id := analysisStatusID(got[0]); id != "123" {
		t.Fatalf("analysisStatusID() = %q, want 123", id)
	}
}

func TestProcessResponseClassifiesOnlyCurrentPost(t *testing.T) {
	const parentText = "親投稿からの依頼：今すぐフォローバックしてほしい"
	const cardTitle = "カードの原文：昨日書いた投稿を全部要約して"
	const authoredText = "どういう意味？"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/statuses/123":
			fmt.Fprintf(w, `{"id":"123","account":{"acct":"other"},"content":%q}`, parentText)
		case "/v1/messages":
			var request struct {
				Messages []struct {
					Content []struct{ Text string }
				}
				Tools []json.RawMessage
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 1 || len(request.Messages[0].Content) == 0 {
				t.Errorf("unexpected LLM request: %+v, %v", request, err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			prompt := request.Messages[0].Content[0].Text
			if len(request.Tools) > 0 {
				if !strings.Contains(prompt, authoredText) || strings.Contains(prompt, parentText) || strings.Contains(prompt, cardTitle) {
					t.Errorf("intent prompt has the wrong context: %s", prompt)
				}
				fmt.Fprint(w, `{"content":[{"type":"tool_use","id":"tool_1","name":"submit_json_result","input":{"result":{"intent":"chat","image_prompt":"","analysis_urls":[],"target_date":""}}}]}`)
				return
			}
			if !strings.Contains(prompt, parentText) || !strings.Contains(prompt, cardTitle) {
				t.Errorf("chat prompt lost the reference context: %s", prompt)
			}
			fmt.Fprint(w, `{"content":[{"type":"text","text":"説明します。"}]}`)
		case "/api/v1/statuses":
			fmt.Fprint(w, `{"id":"125","account":{"acct":"bot"}}`)
		default:
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		BotUsername: "bot", LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1,
		AnthropicBaseURL: server.URL, AnthropicAuthToken: "test-token", AnthropicModel: "test-model",
		MaxPostChars: 500, MaxResponseTokens: 100,
	}
	client, err := llm.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{
		config: cfg, history: &chat.History{}, llmClient: client,
		mastodonClient: mastodon.NewClient(mastodon.Config{Server: server.URL, MaxPostChars: cfg.MaxPostChars}),
		factService:    facts.NewFactService(cfg, nil, nil, nil, nil, nil),
	}
	post := &gomastodon.Notification{
		Account: gomastodon.Account{Acct: "user"},
		Status: &gomastodon.Status{
			ID: "124", InReplyToID: "123", Content: authoredText, Visibility: "public",
			Card: &gomastodon.Card{URL: "https://example.org/", Title: cardTitle},
		},
	}
	if !b.processResponse(t.Context(), &model.Session{}, post, authoredText, "123") {
		t.Fatal("processResponse() failed to post the chat reply")
	}
}

func TestPrepareConversation(t *testing.T) {
	cfg := &config.Config{BotUsername: "bot"}

	b := &Bot{
		config:         cfg,
		mastodonClient: nil,
	}

	ctx := context.Background()
	conv := &model.Conversation{Messages: []model.Message{}}
	notif := &gomastodon.Notification{
		Status: &gomastodon.Status{
			Content:     "Hello",
			InReplyToID: nil,
		},
	}
	userMsg := "Hello"
	statusID := "123"

	resultMsg := b.prepareConversation(ctx, conv, notif, userMsg, statusID, &mentionPages{})

	if resultMsg != "Hello" {
		t.Errorf("Expected 'Hello', got %q", resultMsg)
	}
	if len(conv.Messages) != 1 {
		t.Errorf("Expected 1 message added, got %d", len(conv.Messages))
	}
	if conv.Messages[0].Content != "Hello" {
		t.Errorf("Message content mismatch")
	}
}

func TestMentionReusesFetchedPageForLearning(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<title>Article</title><p>Page body</p>")
	}))
	defer server.Close()
	link := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/article"
	pages := &mentionPages{links: fetcher.ExtractPostURLs(`<a href="` + link + `">truncated...</a>`)}
	if got := pages.reference(t.Context(), nil); !strings.Contains(got, "Page body") {
		t.Fatalf("reference = %q", got)
	}
	meta, err := pages.forLearning(t.Context(), link)
	if err != nil || meta.Content != "Page body" {
		t.Fatalf("learning page = %+v, %v", meta, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("page requested %d times, want once", got)
	}
}

func TestProcessResponseStopsWhenIntentCannotBeDetermined(t *testing.T) {
	var generationCalls, postedNotices atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/messages":
			generationCalls.Add(1)
			_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"unknown"}]}`)
		case "/api/v1/statuses":
			postedNotices.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if got := r.PostForm.Get("status"); !strings.Contains(got, "応答の生成に失敗しました。") {
				t.Errorf("posted notice = %q", got)
			}
			_, _ = io.WriteString(w, `{"id":"125"}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		LLMProvider: config.LLMProviderClaude, LLMMaxConcurrency: 1, EnableFactStore: true,
		AnthropicBaseURL: server.URL, AnthropicAuthToken: "test-token", AnthropicModel: "test-model",
		MaxPostChars: 500, MaxResponseTokens: 100,
	}
	client, err := llm.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{
		config: cfg, llmClient: client,
		mastodonClient: mastodon.NewClient(mastodon.Config{Server: server.URL, MaxPostChars: cfg.MaxPostChars}),
	}
	session := &model.Session{}
	notification := &gomastodon.Notification{
		Account: gomastodon.Account{Acct: "user"},
		Status:  &gomastodon.Status{ID: "124", Content: "こんにちは", Visibility: "public"},
	}
	if bot.processResponse(t.Context(), session, notification, notification.Status.Content, "124") {
		t.Fatal("intent classification failure was treated as a successful response")
	}
	if generationCalls.Load() != 1 || postedNotices.Load() != 1 || len(session.Conversations) != 0 {
		t.Fatalf("LLM calls = %d, posted notices = %d, conversations = %d",
			generationCalls.Load(), postedNotices.Load(), len(session.Conversations))
	}
}

func TestFollowRequestStopsWhenRelationshipLookupFails(t *testing.T) {
	var postedNotices atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/accounts/relationships":
			http.Error(w, "relationship unavailable", http.StatusServiceUnavailable)
		case "/api/v1/statuses":
			postedNotices.Add(1)
			_, _ = io.WriteString(w, `{"id":"125"}`)
		default:
			t.Errorf("unexpected request after relationship lookup failed: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	bot := &Bot{mastodonClient: mastodon.NewClient(mastodon.Config{Server: server.URL, MaxPostChars: 500})}
	notification := &gomastodon.Notification{Account: gomastodon.Account{ID: "123", Acct: "user"}}
	conversation := &model.Conversation{}
	if bot.handleFollowRequest(t.Context(), conversation, notification, "124", "@user ", "public") {
		t.Fatal("relationship lookup failure was treated as a successful follow")
	}
	if postedNotices.Load() != 1 || len(conversation.Messages) != 0 {
		t.Fatalf("notices = %d, messages saved = %d", postedNotices.Load(), len(conversation.Messages))
	}
}
