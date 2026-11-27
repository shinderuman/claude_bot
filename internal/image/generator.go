package image

import (
	"context"
	"fmt"
	"os"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

type ImageGenerator struct {
	config    *config.Config
	llmClient *llm.Client
}

func NewImageGenerator(cfg *config.Config, llmClient *llm.Client) *ImageGenerator {
	return &ImageGenerator{
		config:    cfg,
		llmClient: llmClient,
	}
}

func (g *ImageGenerator) GenerateSVG(ctx context.Context, prompt string) (string, error) {
	if !g.config.EnableImageGeneration {
		return "", fmt.Errorf("画像生成機能が無効です")
	}

	userPrompt := prompts.BuildImageGenerationPrompt(prompt)
	messages := []model.Message{{Role: model.RoleUser, Content: userPrompt}}
	var result struct {
		SVG string `json:"svg"`
	}

	if err := g.llmClient.GenerateJSON(ctx, messages, prompts.Messages.System.ImageGeneration, g.config.MaxImageTokens, "画像生成", &result); err != nil {
		return "", err
	}

	if result.SVG == "" {
		return "", fmt.Errorf("SVGコードが生成されませんでした")
	}

	return result.SVG, nil
}

func (g *ImageGenerator) SaveSVGToFile(svg string, filename string) error {
	return os.WriteFile(filename, []byte(svg), 0644)
}
