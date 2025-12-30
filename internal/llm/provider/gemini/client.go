package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"
	"claude_bot/internal/slack"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	MinProfileResponseLength = 50
	MaxRetries               = 5
)

var (
	retryBaseDelay = 10 * time.Second
)

type Client struct {
	client      *genai.Client
	model       *genai.GenerativeModel
	config      *config.Config
	slackClient *slack.Client
}

func NewClient(cfg *config.Config) provider.Provider {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(cfg.GeminiAPIKey))
	if err != nil {
		log.Fatalf("Geminiクライアント作成エラー: %v", err)
	}

	modelName := cfg.GeminiModel
	model := client.GenerativeModel(modelName)

	return &Client{
		client:      client,
		model:       model,
		config:      cfg,
		slackClient: slack.NewClient(cfg.SlackBotToken, cfg.SlackChannelID, cfg.SlackErrorChannelID, cfg.BotUsername),
	}
}

func (c *Client) GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, error) {
	c.configureModel(systemPrompt, maxTokens, temperature)

	parts, err := c.buildCurrentMessageParts(messages, images)
	if err != nil {
		return "", err
	}

	history := c.buildHistory(messages)
	for i := 0; i <= MaxRetries; i++ {
		cs := c.buildChatSession(history)

		if i > 0 {
			delay := retryBaseDelay * time.Duration(1<<(i-1))
			log.Printf("Gemini リトライ実行 %d/%d (待機: %v)", i, MaxRetries, delay)
			time.Sleep(delay)
		}

		resp, err := cs.SendMessage(ctx, parts...)
		if err != nil {
			log.Printf("Gemini API呼び出しエラー: %v", err)
			return "", err
		}

		responseText, err := c.validateResponse(ctx, resp)
		if err == nil {
			return responseText, nil
		}
	}

	return "", fmt.Errorf("Gemini 生成応答が短すぎます (最大リトライ回数超過)")
}

func (c *Client) configureModel(systemPrompt string, maxTokens int64, temperature float64) {
	// システムプロンプトの設定
	if systemPrompt != "" {
		c.model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(systemPrompt)},
		}
	} else {
		c.model.SystemInstruction = nil
	}

	// トークン上限の設定
	if maxTokens > 0 {
		c.model.SetMaxOutputTokens(int32(maxTokens))
	}
	// Temperatureの設定
	c.model.SetTemperature(float32(temperature))

	c.model.SafetySettings = []*genai.SafetySetting{
		{
			Category:  genai.HarmCategoryHarassment,
			Threshold: genai.HarmBlockNone,
		},
		{
			Category:  genai.HarmCategoryHateSpeech,
			Threshold: genai.HarmBlockNone,
		},
		{
			Category:  genai.HarmCategorySexuallyExplicit,
			Threshold: genai.HarmBlockNone,
		},
		{
			Category:  genai.HarmCategoryDangerousContent,
			Threshold: genai.HarmBlockNone,
		},
	}
}

func (c *Client) buildChatSession(history []*genai.Content) *genai.ChatSession {
	// チャットセッションの開始
	cs := c.model.StartChat()
	cs.History = history
	return cs
}

func (c *Client) buildHistory(messages []model.Message) []*genai.Content {
	// GeminiのHistoryは、最新のメッセージを含まない過去のやり取り
	var history []*genai.Content

	// 最後のメッセージ（ユーザーの新規発言）を除外したものを履歴とする
	if len(messages) > 1 {
		for i := 0; i < len(messages)-1; i++ {
			msg := messages[i]
			role := model.RoleUser
			if msg.Role == model.RoleAssistant {
				role = model.RoleModel
			}

			history = append(history, &genai.Content{
				Role:  role,
				Parts: []genai.Part{genai.Text(msg.Content)},
			})
		}
	}
	return history
}

func (c *Client) buildCurrentMessageParts(messages []model.Message, images []model.Image) ([]genai.Part, error) {
	// 最新メッセージの取得
	if len(messages) == 0 {
		return nil, fmt.Errorf("メッセージが空です")
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
	return parts, nil
}

func (c *Client) validateResponse(ctx context.Context, resp *genai.GenerateContentResponse) (string, error) {
	responseText := extractResponseText(resp)
	runeCount := utf8.RuneCountInString(responseText)

	if runeCount > MinProfileResponseLength {
		return responseText, nil
	}

	isProfileGeneration, _ := ctx.Value(model.ContextKeyIsProfileGeneration).(bool)
	if !isProfileGeneration {
		return responseText, nil
	}

	var finishReason genai.FinishReason
	if len(resp.Candidates) > 0 {
		finishReason = resp.Candidates[0].FinishReason
	}

	msg := fmt.Sprintf("⚠️ [生成異常] Geminiが短い応答を返しました (%d文字, Reason: %s)\n```\n%s\n```\nリトライします...", runeCount, finishReason, responseText)
	c.slackClient.PostErrorMessageAsync(ctx, msg)

	return "", fmt.Errorf("response too short")
}

func (c *Client) IsRetryable(err error) bool {
	if gerr, ok := err.(*googleapi.Error); ok {
		return gerr.Code == http.StatusTooManyRequests || gerr.Code >= http.StatusInternalServerError
	}
	return false
}

func extractResponseText(resp *genai.GenerateContentResponse) string {
	if len(resp.Candidates) > 0 {
		var result strings.Builder
		for _, part := range resp.Candidates[0].Content.Parts {
			if txt, ok := part.(genai.Text); ok {
				result.WriteString(string(txt))
			}
		}
		return result.String()
	}
	return ""
}
