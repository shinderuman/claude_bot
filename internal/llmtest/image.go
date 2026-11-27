package llmtest

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/image"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

func runRawImage(cfg *config.Config, client *llm.Client, message, imagePath string) error {
	log.Printf("=== 画像認識テスト（最小プロンプト） ===")
	if cfg.LLMProvider != config.LLMProviderOpenAI {
		log.Printf("テストメッセージ: %s", message)
	}
	if imagePath == "" {
		return errors.New("エラー: 画像ファイルパスが必要です (-image オプション)")
	}
	log.Printf("画像ファイル: %s", imagePath)
	log.Println()

	img, err := loadImage(imagePath)
	if err != nil {
		return fmt.Errorf("画像読み込みエラー: %w", err)
	}
	currentImages := []model.Image{*img}

	messages := []model.Message{{Role: "user", Content: message}}

	ctx := context.Background()
	response := client.GenerateText(ctx, messages, "", cfg.MaxResponseTokens, currentImages, cfg.LLMTemperature, "画像入力への応答生成")

	if response == "" {
		return errEmptyReply
	}

	logReply(response)
	return nil
}

func loadImage(path string) (*model.Image, error) {
	data, err := os.ReadFile(path)
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

func runGenerateImage(cfg *config.Config, imageGen *image.ImageGenerator, prompt string) error {
	log.Printf("=== SVG画像生成テスト ===")
	if cfg.LLMProvider != config.LLMProviderOpenAI {
		log.Printf("プロンプト: %s", prompt)
		log.Println()
	}

	ctx := context.Background()
	svg, err := imageGen.GenerateSVG(ctx, prompt)
	if err != nil {
		return fmt.Errorf("画像生成エラー: %w", err)
	}

	filename := fmt.Sprintf("generated_image_%d.svg", time.Now().Unix())
	if err := imageGen.SaveSVGToFile(svg, filename); err != nil {
		return fmt.Errorf("ファイル保存エラー: %w", err)
	}

	log.Printf("画像保存: %s", filename)
	log.Printf("ファイルサイズ: %d bytes", len(svg))

	encoded := base64.StdEncoding.EncodeToString([]byte(svg))
	base64Filename := fmt.Sprintf("generated_image_%d.base64.txt", time.Now().Unix())
	if err := os.WriteFile(base64Filename, []byte(encoded), 0644); err != nil {
		log.Printf("Base64ファイル保存失敗: %v", err)
	} else {
		log.Printf("Base64版保存: %s", base64Filename)
	}
	return nil
}
