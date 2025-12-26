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
	// モックサーバーの設定
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

		// 検証ポイント
		if len([]rune(content)) > 500 {
			t.Errorf("Content length exceeds 500: %d", len([]rune(content)))
		}
		if !strings.HasSuffix(content, BotTag) {
			t.Errorf("Content does not end with BotTag. Got suffix: %s", content[len(content)-10:])
		}

		// レスポンス (最小限)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"id": "12345", "content": "posted"}`)
	}))
	defer ts.Close()

	// クライアント初期化
	cfg := Config{
		Server:       ts.URL,
		AccessToken:  "token",
		MaxPostChars: 500,
	}
	c := NewClient(cfg)

	// テストケース
	tests := []struct {
		name    string
		content string
		wantEnd string // BotTagの直前の文字など
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
			name: "Exceed limit (Japanese period)",
			// 480文字程度のあとに句点を入れて、その後にさらに文字を続ける
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
			// サーバー側で検証済みだが、ここでも追加検証が必要なら実装可能
			// ただし、実際に何が送られたかはサーバー側でしかわからないため、
			// 厳密な値チェックはモックサーバー内のロジックでカバーするか、
			// チャネルを使って送信値をテスト側に渡すのが典型的。
			// 今回は簡易的にサーバー内でチェックし、エラーがあればt.Errorする形をとっている。
			// 複数のリクエストが来るので、テストケースごとに検証ロジックを切り替えるのは少し複雑。
			// よって、共通の不変条件（長さ、タグ）のみサーバーでチェックしている。
		})
	}
}

// より詳細な値を検証するためのテスト
func TestPostStatus_ContentVerification(t *testing.T) {
	var receivedContent string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		receivedContent = r.FormValue("status")
		fmt.Fprintln(w, `{"id": "1", "content": "ok"}`)
	}))
	defer ts.Close()

	cfg := Config{Server: ts.URL, AccessToken: "so", MaxPostChars: 20} // 短く設定
	c := NewClient(cfg)
	// BotTag len is 7 (\n\n#bot)
	// Max 20. Limit for text = 13.

	input := "123456789012345" // 15 chars
	// Limit is 14. "12345678901234"

	_, err := c.PostStatus(context.Background(), input, "public")
	if err != nil {
		t.Fatalf("PostStatus error: %v", err)
	}

	expected := "12345678901234" + BotTag
	if receivedContent != expected {
		t.Errorf("Content mismatch.\nGot: %q\nWant: %q", receivedContent, expected)
	}
}
