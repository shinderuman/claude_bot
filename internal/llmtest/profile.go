package llmtest

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"

	"claude_bot/internal/config"
	"claude_bot/internal/mastodon"
)

func runProfileUpdate(cfg *config.Config) error {
	log.Println("=== プロフィール更新テスト（モックモード） ===")
	log.Println()

	ts := httptest.NewServer(http.HandlerFunc(serveProfileTestRequest))
	defer ts.Close()
	log.Printf("[MockServer] Started at %s", ts.URL)

	mockConfig := mastodon.Config{
		Server:           ts.URL,
		AccessToken:      "dummy_token",
		BotUsername:      "bot",
		AllowRemoteUsers: true,
		MaxPostChars:     cfg.MaxPostChars,
	}
	mockClient := mastodon.NewClient(mockConfig)

	dummyNoteRaw := "これはテスト用のプロフィール本文です。\n\n整形前です。"
	dummyAuthKey := "dummy_auth_key"

	ctx := context.Background()

	formattedBody := mockClient.FormatProfileBody(dummyNoteRaw)
	safeBody := mockClient.TruncateToSafeProfileBody(formattedBody)

	log.Println("UpdateProfileWithFields呼び出し（シミュレーション）...")
	if err := mockClient.UpdateProfileWithFields(ctx, cfg, safeBody, dummyAuthKey); err != nil {
		return fmt.Errorf("UpdateProfileWithFieldsエラー: %w", err)
	}

	log.Println("PostStatus呼び出し（シミュレーション）...")
	if _, err := mockClient.PostStatus(ctx, safeBody, cfg.AutoPostVisibility); err != nil {
		return fmt.Errorf("PostStatusエラー: %w", err)
	}

	log.Println()
	log.Println("モックへのプロフィール更新・投稿完了")
	return nil
}

func serveProfileTestRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("[MockServer] Request Received: %s %s", r.Method, r.URL.Path)
	var response string
	switch r.Method + " " + r.URL.Path {
	case "PATCH /api/v1/accounts/update_credentials":
		log.Println("[MockServer] プロフィール更新リクエスト受信成功")
		response = `{"id": "123456", "username": "bot", "note": "updated profile"}`
	case "GET /api/v1/accounts/verify_credentials":
		log.Println("[MockServer] ユーザー情報確認リクエスト受信成功")
		response = `{"id": "123456", "username": "bot", "display_name": "Test Bot", "fields": []}`
	case "POST /api/v1/statuses":
		log.Println("[MockServer] ステータス投稿リクエスト受信成功")
		response = `{"id": "789012", "content": "status posted"}`
	default:
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("[MockServer] Body: %s", body)
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(w, response)
}
