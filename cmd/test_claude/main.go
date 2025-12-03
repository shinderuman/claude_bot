package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

func main() {
	mode := flag.String("mode", "response", "テストモード: response, summary, fact")
	message := flag.String("message", "Hello", "テストメッセージ")
	imagePath := flag.String("image", "", "画像ファイルパス (responseモード用)")
	existingSummary := flag.String("existing-summary", "", "既存の要約（summaryモード用）")
	flag.Parse()

	config.LoadEnvironment()
	cfg := config.LoadConfig()

	// 設定情報を出力
	printConfig(cfg)

	llmClient := llm.NewClient(cfg)

	switch *mode {
	case "response":
		testResponse(cfg, llmClient, *message, *imagePath)
	case "summary":
		testSummary(cfg, llmClient, *message, *existingSummary)
	case "fact":
		testFactExtraction(cfg, llmClient, *message)
	default:
		log.Fatalf("不明なモード: %s (使用可能: response, summary, fact)", *mode)
	}
}

func printConfig(cfg *config.Config) {
	log.Println("=== 設定情報 ===")
	log.Printf("Mastodonサーバー: %s", cfg.MastodonServer)
	log.Printf("Botユーザー名: @%s", cfg.BotUsername)
	log.Printf("Claude API: %s", cfg.AnthropicBaseURL)
	log.Printf("Claudeモデル: %s", cfg.AnthropicModel)
	log.Printf("リモートユーザー許可: %t", cfg.AllowRemoteUsers)
	log.Printf("事実ストア有効: %t", cfg.EnableFactStore)
	log.Println()
	log.Println("=== 会話管理設定 ===")
	log.Printf("メッセージ圧縮しきい値: %d件", cfg.ConversationMessageCompressThreshold)
	log.Printf("保持メッセージ数: %d件", cfg.ConversationMessageKeepCount)
	log.Printf("アイドル時間: %d時間", cfg.ConversationIdleHours)
	log.Printf("保持時間: %d時間", cfg.ConversationRetentionHours)
	log.Printf("最小保持数: %d件", cfg.ConversationMinKeepCount)
	log.Println()
	log.Println("=== LLM & 投稿設定 ===")
	log.Printf("最大応答トークン: %d", cfg.MaxResponseTokens)
	log.Printf("最大要約トークン: %d", cfg.MaxSummaryTokens)
	log.Printf("最大投稿文字数: %d", cfg.MaxPostChars)
	log.Println()
	log.Println("==================")
	log.Println()
}

func testResponse(cfg *config.Config, client *llm.Client, message, imagePath string) {
	log.Printf("=== 通常応答テスト ===")
	log.Printf("テストメッセージ: %s", message)
	if imagePath != "" {
		log.Printf("画像ファイル: %s", imagePath)
	}
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// テスト用セッション作成
	session := &model.Session{
		Conversations: []model.Conversation{},
		Summary:       "",
		LastUpdated:   time.Now(),
	}
	conversation := &model.Conversation{
		RootStatusID: "test",
		CreatedAt:    time.Now(),
		LastUpdated:  time.Now(),
		Messages:     []model.Message{{Role: "user", Content: message}},
	}
	session.Conversations = append(session.Conversations, *conversation)

	var currentImages []model.Image
	if imagePath != "" {
		img, err := loadImage(imagePath)
		if err != nil {
			log.Fatalf("画像読み込みエラー: %v", err)
		}
		currentImages = append(currentImages, *img)
	}

	ctx := context.Background()
	response := client.GenerateResponse(ctx, nil, conversation, "", currentImages)

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	log.Println("成功: Claudeから応答を受信しました")
	log.Println()
	log.Println("--- Claude応答 ---")
	log.Println(response)
	log.Println("------------------")
}

func testSummary(cfg *config.Config, client *llm.Client, newMessages, existingSummary string) {
	log.Printf("=== 要約生成テスト ===")
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// 新しいメッセージをフォーマット
	formattedMessages := newMessages

	// 要約プロンプトを構築
	summaryPrompt := llm.BuildSummaryPrompt(formattedMessages, existingSummary)

	log.Println("--- 要約プロンプト ---")
	log.Println(summaryPrompt)
	log.Println("--------------------")
	log.Println()

	// 要約生成
	messages := []model.Message{{Role: "user", Content: summaryPrompt}}
	ctx := context.Background()
	summary := client.CallClaudeAPIForSummary(ctx, messages, existingSummary)

	if summary == "" {
		log.Fatal("エラー: 要約生成に失敗しました")
	}

	log.Println("成功: 要約を生成しました")
	log.Println()
	log.Println("--- 生成された要約 ---")
	log.Println(summary)
	log.Println("---------------------")
	log.Printf("要約の長さ: %d文字", len(summary))
}

func testFactExtraction(cfg *config.Config, client *llm.Client, message string) {
	log.Printf("=== 事実抽出テスト ===")
	log.Printf("テストメッセージ: %s", message)
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// 事実抽出プロンプトを構築
	authorUserName := "testuser"
	author := "testuser@example.com"
	prompt := llm.BuildFactExtractionPrompt(authorUserName, author, message)

	log.Println("--- 事実抽出プロンプト ---")
	log.Println(prompt)
	log.Println("------------------------")
	log.Println()

	// 事実抽出
	messages := []model.Message{{Role: "user", Content: prompt}}
	ctx := context.Background()
	response := client.CallClaudeAPI(ctx, messages, llm.SystemPromptFactExtraction, cfg.MaxResponseTokens, nil)

	if response == "" {
		log.Fatal("エラー: 事実抽出に失敗しました")
	}

	log.Println("成功: 事実を抽出しました")
	log.Println()
	log.Println("--- 抽出された事実 (JSON) ---")
	log.Println(response)
	log.Println("----------------------------")
}

func loadImage(path string) (*model.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, fmt.Errorf("not an image: %s", mimeType)
	}

	log.Printf("画像読み込み成功: MIMEタイプ=%s, サイズ=%d bytes", mimeType, len(data))

	return &model.Image{
		Data:      base64.StdEncoding.EncodeToString(data),
		MediaType: mimeType,
	}, nil
}
