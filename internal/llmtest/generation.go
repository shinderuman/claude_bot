package llmtest

import (
	"context"
	"errors"
	"fmt"
	"log"

	"claude_bot/internal/config"
	"claude_bot/internal/conversation"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
	"claude_bot/internal/store"
)

func runSummary(cfg *config.Config, client *llm.Client, newMessages, existingSummary string) error {
	log.Printf("=== 要約生成テスト ===")
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		return errMissingAnthropicToken
	}

	summaryPrompt := prompts.BuildSummaryPrompt(newMessages, existingSummary)

	log.Println("--- 要約プロンプト ---")
	log.Println(summaryPrompt)
	log.Println("--------------------")
	log.Println()

	messages := []model.Message{{Role: model.RoleUser, Content: summaryPrompt}}
	ctx := context.Background()
	summary := client.GenerateSummary(ctx, messages, existingSummary)

	if summary == "" {
		return errors.New("エラー: 要約生成に失敗しました")
	}

	log.Println("要約生成完了")
	log.Println()
	log.Println("--- 生成された要約 ---")
	log.Println(summary)
	log.Println("---------------------")
	log.Printf("要約の長さ: %d文字", len(summary))
	return nil
}

func runFactExtraction(cfg *config.Config, client *llm.Client, message string) error {
	log.Printf("=== 事実抽出テスト ===")
	log.Printf("テストメッセージ: %s", message)
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		return errMissingAnthropicToken
	}

	authorUserName := "testuser"
	author := "testuser@example.com"
	prompt := prompts.BuildFactExtractionPrompt(authorUserName, author, message, "test_bot", false)

	log.Println("--- 事実抽出プロンプト ---")
	log.Println(prompt)
	log.Println("------------------------")
	log.Println()

	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}
	ctx := context.Background()
	response := client.GenerateText(ctx, messages, prompts.Messages.System.FactExtraction, cfg.MaxResponseTokens, nil, llm.TemperatureSystem)

	if response == "" {
		return errors.New("エラー: 事実抽出に失敗しました")
	}

	log.Println("ファクト抽出完了")
	log.Println()
	log.Println("--- 抽出された事実 (JSON) ---")
	log.Println(response)
	log.Println("----------------------------")
	return nil
}

func runAutoPost(cfg *config.Config, client *llm.Client) error {
	log.Printf("=== 自動投稿テスト ===")
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		return errMissingAnthropicToken
	}

	if _, err := conversation.Open(cfg); err != nil {
		return err
	}

	factStore, err := store.InitializeFactStore(cfg)
	if err != nil {
		return err
	}
	defer factStore.Close()

	facts, err := factStore.GetRandomGeneralFactBundle(context.Background(), 5)
	if err != nil {
		return fmt.Errorf("ファクト取得エラー: %w", err)
	}
	if len(facts) == 0 {
		return errors.New("エラー: 一般知識のファクトが見つかりません。facts.jsonを確認してください。")
	}

	log.Printf("取得したファクト数: %d件", len(facts))
	log.Printf("情報源: %s", facts[0].TargetUserName)
	for _, f := range facts {
		log.Printf("- %s: %v", f.Key, f.Value)
	}
	log.Println()

	prompt := prompts.BuildAutoPostPrompt(facts)
	log.Println("--- 生成されたプロンプト ---")
	log.Println(prompt)
	log.Println("--------------------------")
	log.Println()

	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}
	response, err := generateInCharacter(cfg, client, messages, int64(cfg.MaxPostChars))
	if err != nil {
		return err
	}

	log.Println("自動投稿文生成完了")
	log.Println()
	log.Println("--- 生成された投稿文 ---")
	log.Println(response)
	log.Println("----------------------")
	log.Printf("文字数: %d文字", len([]rune(response)))
	return nil
}

func runErrorMessageGeneration(cfg *config.Config, client *llm.Client, errorDetail string) error {
	log.Printf("=== エラーメッセージ生成テスト ===")
	log.Printf("エラー詳細: %s", errorDetail)
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		return errMissingAnthropicToken
	}

	prompt := prompts.BuildErrorMessagePrompt(errorDetail)
	log.Println("--- 生成されたプロンプト ---")
	log.Println(prompt)
	log.Println("--------------------------")
	log.Println()

	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}
	response, err := generateInCharacter(cfg, client, messages, cfg.MaxResponseTokens)
	if err != nil {
		return err
	}

	log.Println("エラーメッセージ生成完了")
	log.Println()
	log.Println("--- 生成されたメッセージ ---")
	log.Println(response)
	log.Println("--------------------------")
	return nil
}

func generateInCharacter(cfg *config.Config, client *llm.Client, messages []model.Message, maxTokens int64) (string, error) {
	systemPrompt := prompts.BuildSystemPrompt(cfg, "", "", "", true, cfg.CharacterPriority)
	response := client.GenerateText(context.Background(), messages, systemPrompt, maxTokens, nil, cfg.LLMTemperature)
	if response == "" {
		return "", errEmptyClaudeReply
	}
	return response, nil
}
