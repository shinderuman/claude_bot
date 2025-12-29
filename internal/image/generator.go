package image

import (
	"context"
	"fmt"
	"os"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
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

// GenerateSVG generates an SVG image based on the given prompt
func (g *ImageGenerator) GenerateSVG(ctx context.Context, prompt string) (string, error) {
	if !g.config.EnableImageGeneration {
		return "", fmt.Errorf("画像生成機能が無効です")
	}

	userPrompt := llm.BuildImageGenerationPrompt(prompt)
	messages := []model.Message{{Role: model.RoleUser, Content: userPrompt}}
	response := g.llmClient.GenerateText(ctx, messages, llm.Messages.System.ImageGeneration, g.config.MaxImageTokens, nil, llm.TemperatureSystem)

	if response == "" {
		return "", fmt.Errorf("LLMからの応答がありません")
	}

	// JSONをパース
	jsonStr := llm.ExtractJSON(response)
	var result struct {
		SVG string `json:"svg"`
	}

	if err := llm.UnmarshalWithRepair(jsonStr, &result, "画像生成"); err != nil {
		return "", fmt.Errorf("JSONパースエラー: %v", err)
	}

	if result.SVG == "" {
		return "", fmt.Errorf("SVGコードが生成されませんでした")
	}

	return result.SVG, nil
}

// SaveSVGToFile saves SVG content to a file
func (g *ImageGenerator) SaveSVGToFile(svg string, filename string) error {
	return os.WriteFile(filename, []byte(svg), 0644)
}
