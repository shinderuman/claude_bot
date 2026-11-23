package llmtest

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/facts"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

func runResponse(cfg *config.Config, client *llm.Client, factService *facts.FactService, message, imagePath string) error {
	log.Printf("=== 通常応答テスト ===")
	log.Printf("テストメッセージ: %s", message)
	if imagePath != "" {
		log.Printf("画像ファイル: %s", imagePath)
	}
	log.Println()

	testUser := "asmodeus"
	testUserName := "グレートマグマカッター"

	session := &model.Session{
		Conversations: []model.Conversation{},
		Summary:       "",
		LastUpdated:   time.Now(),
	}
	conversation := &model.Conversation{
		RootStatusID: "test",
		CreatedAt:    time.Now(),
		LastUpdated:  time.Now(),
		Messages:     []model.Message{{Role: model.RoleUser, Content: message}},
	}
	session.Conversations = append(session.Conversations, *conversation)

	var currentImages []model.Image
	if imagePath != "" {
		img, err := loadImage(imagePath)
		if err != nil {
			return fmt.Errorf("画像読み込みエラー: %w", err)
		}
		currentImages = append(currentImages, *img)
	}

	ctx := context.Background()

	log.Println("事実を検索中...")
	relevantFacts := factService.QueryRelevantFacts(ctx, testUser, testUserName, message)
	if relevantFacts != "" {
		log.Println("--- 関連する事実 ---")
		log.Println(relevantFacts)
		log.Println("------------------")
	} else {
		log.Println("関連する事実は見つかりませんでした")
	}

	var botProfile string
	if cfg.BotProfileFile != "" {
		if content, err := os.ReadFile(cfg.BotProfileFile); err == nil {
			botProfile = string(content)
		} else {
			log.Printf("Botプロフィール読込失敗、続行: %v", err)
		}
	}

	response := client.GenerateResponse(ctx, session, conversation, relevantFacts, botProfile, currentImages)

	if response == "" {
		return errEmptyClaudeReply
	}

	log.Println("Claude応答受信")
	log.Println()
	log.Println("--- Claude応答 ---")
	log.Println(response)
	log.Println("------------------")

	if cfg.EnableFactStore {
		log.Println()
		log.Println("ファクトを抽出中...")
		baseFact := model.Fact{
			SourceID:           "test_source_id",
			Author:             "user",
			AuthorUserName:     "TestUser",
			SourceType:         model.SourceTypeTest,
			PostAuthor:         "test_post_author",
			PostAuthorUserName: "TestPostUserName",
			IsTrusted:          false,
		}
		factService.ExtractAndSaveFacts(ctx, message, baseFact)
		log.Println("ファクト抽出完了")
	}
	return nil
}

func runConversation(cfg *config.Config, client *llm.Client, lastMessage string) error {
	log.Printf("=== 会話履歴テスト ===")
	log.Println()

	messages := []model.Message{
		{Role: "user", Content: "ねぇルナ、今日の晩御飯なにがいいと思う？"},
		{Role: "assistant", Content: "ふん、ご主人様ったら、そんなことも自分で決められないのですか？...まあ、今日は魔力が満ちているので、激辛麻婆豆腐なんていかがでしょう。深淵なる赤色が食欲をそそりますわよ。"},
		{Role: "user", Content: lastMessage},
	}

	log.Println("--- 会話履歴 ---")
	for _, msg := range messages {
		log.Printf("[%s]: %s", msg.Role, msg.Content)
	}
	log.Println("----------------")
	log.Println()

	response, err := generateInCharacter(cfg, client, messages, cfg.MaxResponseTokens)
	if err != nil {
		return err
	}

	log.Println("応答受信")
	log.Println()
	log.Println("--- 応答 ---")
	log.Println(response)
	log.Println("------------------")
	return nil
}
