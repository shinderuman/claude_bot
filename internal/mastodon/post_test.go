package mastodon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostStatus_Truncation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/statuses" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
			return
		}
		if r.Method != "POST" {
			t.Errorf("Unexpected method: %s", r.Method)
			return
		}

		if err := r.ParseForm(); err != nil {
			t.Errorf("Failed to parse form: %v", err)
			return
		}

		content := r.FormValue("status")

		if len([]rune(content)) > 500 {
			t.Errorf("Content length exceeds 500: %d", len([]rune(content)))
		}
		if !strings.HasSuffix(content, BotTag) {
			t.Errorf("Content does not end with BotTag. Got suffix: %s", content[len(content)-10:])
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"id": "12345", "content": "posted"}`)
	}))
	defer ts.Close()

	cfg := Config{
		Server:       ts.URL,
		AccessToken:  "token",
		MaxPostChars: 500,
	}
	c := NewClient(cfg)

	tests := []struct {
		name    string
		content string
		wantEnd string
	}{
		{
			name:    "Short content",
			content: "Short message",
			wantEnd: "Short message",
		},
		{
			name:    "Exact limit (minus tag)",
			content: strings.Repeat("a", 500-len([]rune(BotTag))),
			wantEnd: "a",
		},
		{
			name:    "Exceed limit (simple cut)",
			content: strings.Repeat("a", 600),
			wantEnd: "a",
		},
		{
			name:    "Exceed limit (Japanese period)",
			content: strings.Repeat("あ", 480) + "終わり。ここはカットされるはず",
			wantEnd: "終わり。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.PostStatus(context.Background(), tt.content, "public")
			if err != nil {
				t.Errorf("PostStatus failed: %v", err)
			}
		})
	}
}

func TestPostStatus_ContentVerification(t *testing.T) {
	var receivedContent string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		receivedContent = r.FormValue("status")
		fmt.Fprintln(w, `{"id": "1", "content": "ok"}`)
	}))
	defer ts.Close()

	cfg := Config{Server: ts.URL, AccessToken: "so", MaxPostChars: 20}
	c := NewClient(cfg)

	input := "123456789012345"

	_, err := c.PostStatus(context.Background(), input, "public")
	if err != nil {
		t.Fatalf("PostStatus error: %v", err)
	}

	expected := "12345678901234" + BotTag
	if receivedContent != expected {
		t.Errorf("Content mismatch.\nGot: %q\nWant: %q", receivedContent, expected)
	}
}
