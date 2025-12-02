package main

import (
	"context"
	"flag"
	"log"
	"time"

	"claude_bot/internal/bot"
	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

func main() {
	message := flag.String("message", "Hello", "テストメッセージ")
	flag.Parse()

	config.LoadEnvironment()
	cfg := config.LoadConfig()

	// Set the system prompt builder from bot package
	llm.SetSystemPromptBuilder(bot.BuildSystemPrompt)

	llmClient := llm.NewClient(cfg)

	runTestMode(cfg, llmClient, *message)
}

func runTestMode(cfg *config.Config, client *llm.Client, message string) {
	log.Printf("Claude API疎通確認開始")
	log.Printf("エンドポイント: %s", cfg.AnthropicBaseURL)
	log.Printf("モデル: %s", cfg.AnthropicModel)
	log.Printf("テストメッセージ: %s", message)
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
		Messages:     []model.Message{{Role: "user", Content: message}},
	}
	session.Conversations = append(session.Conversations, *conversation)

	ctx := context.Background()
	response := client.GenerateResponse(ctx, session, conversation, "")

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	log.Println("成功: Claudeから応答を受信しました")
	log.Println()
	log.Println("--- Claude応答 ---")
	log.Println(response)
	log.Println("------------------")
}
