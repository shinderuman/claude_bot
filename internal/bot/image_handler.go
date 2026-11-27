package bot

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	chat "claude_bot/internal/conversation"
	"claude_bot/internal/image"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func (b *Bot) handleImageGeneration(ctx context.Context, session *model.Session, conversation *model.Conversation, imagePrompt, statusID, mention, visibility string) bool {
	svg, err := b.imageGenerator.GenerateSVG(ctx, imagePrompt)
	if err != nil {
		log.Printf("画像生成失敗: %v", err)

		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.ImageGeneration)
		return false
	}

	tempDir, err := os.MkdirTemp("", "claude-bot-image-")
	if err != nil {
		log.Printf("画像用一時ディレクトリ作成失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.Internal)
		return false
	}
	defer os.RemoveAll(tempDir)

	svgPath := filepath.Join(tempDir, "image.svg")
	if err := os.WriteFile(svgPath, []byte(svg), 0644); err != nil {
		log.Printf("SVG保存失敗: %v", err)

		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.Internal)
		return false
	}
	pngPath := filepath.Join(tempDir, "image.png")
	if err := image.ConvertSVGToPNG(svgPath, pngPath); err != nil {
		log.Printf("PNG変換失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.ImageGeneration)
		return false
	}

	replyPrompt := prompts.BuildImageGenerationReplyPrompt(imagePrompt, b.config.CharacterPrompt)
	replyMessages := []model.Message{{Role: model.RoleUser, Content: replyPrompt}}
	response := b.llmClient.GenerateText(ctx, replyMessages, "", b.config.MaxResponseTokens, nil, b.config.LLMTemperature, "画像への添え文生成")

	if response == "" {
		response = prompts.Messages.Success.ImageGeneration
	}

	postedID, err := b.mastodonClient.PostResponseWithMedia(ctx, statusID, mention, response, visibility, pngPath)
	if err != nil {
		log.Printf("画像返信失敗: %v", err)

		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.ImagePost)
		return false
	}

	chat.AddMessage(conversation, model.RoleAssistant, response, []string{postedID})

	session.LastUpdated = time.Now()
	return true
}
