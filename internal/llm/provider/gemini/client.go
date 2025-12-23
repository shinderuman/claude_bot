package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type Client struct {
	client *genai.Client
	model  *genai.GenerativeModel
	config *config.Config
}

func NewClient(cfg *config.Config) provider.Provider {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(cfg.GeminiAPIKey))
	if err != nil {
		log.Fatalf("Geminiクライアント作成エラー: %v", err)
	}

	modelName := cfg.GeminiModel
	if modelName == "" {
		modelName = "gemini-1.5-pro"
	}
	model := client.GenerativeModel(modelName)

	return &Client{
		client: client,
		model:  model,
		config: cfg,
	}
}

func (c *Client) GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, error) {
	// システムプロンプトの設定
	if systemPrompt != "" {
		c.model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(systemPrompt)},
		}
	} else {
		c.model.SystemInstruction = nil
	}

	// トークン上限の設定
	// GeminiはMaxOutputTokensで設定
	if maxTokens > 0 {
		c.model.SetMaxOutputTokens(int32(maxTokens))
	}
	// Temperatureの設定
	c.model.SetTemperature(float32(temperature))

	// チャットセッションの開始
	cs := c.model.StartChat()

	// 履歴の変換と追加
	// GeminiのHistoryは、最新のメッセージを含まない過去のやり取り
	var history []*genai.Content

	// 最後のメッセージ（ユーザーの新規発言）を除外したものを履歴とする
	if len(messages) > 1 {
		for i := 0; i < len(messages)-1; i++ {
			msg := messages[i]
			role := "user"
			if msg.Role == "assistant" {
				role = "model"
			}

			history = append(history, &genai.Content{
				Role:  role,
				Parts: []genai.Part{genai.Text(msg.Content)},
			})
		}
		cs.History = history
	}

	// 最新メッセージの送信
	if len(messages) == 0 {
		return "", fmt.Errorf("メッセージが空です")
	}

	lastMsg := messages[len(messages)-1]

	var parts []genai.Part
	parts = append(parts, genai.Text(lastMsg.Content))

	// 画像の添付
	if len(images) > 0 {
		for _, img := range images {
			// Base64デコード
			data, err := base64.StdEncoding.DecodeString(img.Data)
			if err != nil {
				log.Printf("画像デコード警告: %v", err)
				continue
			}

			// MIMEタイプ処理
			parts = append(parts, genai.ImageData(img.MediaType, data))
		}
	}

	// 生成実行
	resp, err := cs.SendMessage(ctx, parts...)
	if err != nil {
		log.Printf("Gemini API呼び出しエラー: %v", err)
		return "", err
	}

	return extractResponseText(resp), nil
}

func extractResponseText(resp *genai.GenerateContentResponse) string {
	if len(resp.Candidates) > 0 {
		var result string
		for _, part := range resp.Candidates[0].Content.Parts {
			if txt, ok := part.(genai.Text); ok {
				result += string(txt)
			}
		}
		return result
	}
	return ""
}
