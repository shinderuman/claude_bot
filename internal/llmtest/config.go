package llmtest

import (
	"log"

	"claude_bot/internal/config"
)

func printConfig(cfg *config.Config) {
	if cfg.LLMProvider == config.LLMProviderOpenAI {
		log.Printf("OpenAIモデル: %s", cfg.OpenAIModel)
		return
	}
	log.Println("=== 設定情報 ===")
	log.Printf("Mastodonサーバー: %s", cfg.MastodonServer)
	log.Printf("Botユーザー名: @%s", cfg.BotUsername)
	log.Printf("リモートユーザー許可: %t", cfg.AllowRemoteUsers)
	log.Printf("事実ストア有効: %t", cfg.EnableFactStore)
	log.Printf("画像認識有効: %t", cfg.EnableImageRecognition)
	log.Println()
	log.Println("=== 会話管理設定 ===")
	log.Printf("メッセージ圧縮しきい値: %d件", cfg.ConversationMessageCompressThreshold)
	log.Printf("保持メッセージ数: %d件", cfg.ConversationMessageKeepCount)
	log.Printf("アイドル時間: %d時間", cfg.ConversationIdleHours)
	log.Printf("保持時間: %d時間", cfg.ConversationRetentionHours)
	log.Printf("最小保持数: %d件", cfg.ConversationMinKeepCount)
	log.Println()
	log.Println("=== LLM & 投稿設定 ===")
	log.Printf("プロバイダー: %s", cfg.LLMProvider)
	switch cfg.LLMProvider {
	case "claude":
		log.Printf("Claude API: %s", cfg.AnthropicBaseURL)
		log.Printf("Claudeモデル: %s", cfg.AnthropicModel)
	case "gemini":
		log.Printf("Geminiモデル: %s", cfg.GeminiModel)
	}
	log.Printf("最大応答トークン: %d", cfg.MaxResponseTokens)
	log.Printf("最大要約トークン: %d", cfg.MaxSummaryTokens)
	log.Printf("最大ファクトトークン: %d", cfg.MaxFactTokens)
	log.Printf("最大画像生成トークン: %d", cfg.MaxImageTokens)
	log.Printf("最大投稿文字数: %d", cfg.MaxPostChars)
	log.Println()
	log.Println("=== ファクト収集設定 ===")
	log.Printf("ファクト収集有効: %t", cfg.FactCollectionEnabled)
	if cfg.FactCollectionEnabled {
		log.Printf("連合タイムライン: %t", cfg.FactCollectionFederated)
		log.Printf("ホームタイムライン: %t", cfg.FactCollectionHome)
		log.Printf("投稿本文から収集: %t", cfg.FactCollectionFromPostContent)
		log.Printf("最大並列処理数: %d", cfg.FactCollectionMaxWorkers)
		log.Printf("1時間あたり最大処理数: %d", cfg.FactCollectionMaxPerHour)
	}
	log.Println()
	log.Println("==================")
	log.Println()
}
