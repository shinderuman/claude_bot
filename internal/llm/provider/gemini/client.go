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

	client, err := genai.NewClient(ctx,
		option.WithAPIKey(cfg.GeminiAPIKey),
	)
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

func (c *Client) GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error) {
	requestModel := c.requestModel(systemPrompt, maxTokens, temperature)

	parts, err := c.buildRequestParts(messages, images)
	if err != nil {
		return "", "", err
	}

	for i := 0; i <= MaxRetries; i++ {
		if i > 0 {
			delay := retryBaseDelay * time.Duration(1<<(i-1))
			log.Printf("Gemini リトライ実行 %d/%d (待機: %v)", i, MaxRetries, delay)
			time.Sleep(delay)
		}

		resp, err := requestModel.GenerateContent(ctx, parts...)
		if err != nil {
			log.Printf("Gemini API呼び出しエラー: %v", err)
			return "", "", err
		}

		responseText, err := c.validateResponse(ctx, resp)
		if err == nil {
			return responseText, "", nil
		}
	}

	return "", "", fmt.Errorf("Gemini 生成応答が短すぎます (最大リトライ回数超過)")
}

func (c *Client) requestModel(systemPrompt string, maxTokens int64, temperature float64) *genai.GenerativeModel {
	requestModel := *c.model
	configureModel(&requestModel, systemPrompt, maxTokens, temperature)
	return &requestModel
}

// ChatSession(SendMessage)は内部でストリーミングAPIを使用し、
// 現行モデルではレスポンスのパースに失敗するため使用しない。
// 履歴はプロンプトへの埋め込みで送信する。
func (c *Client) buildRequestParts(messages []model.Message, images []model.Image) ([]genai.Part, error) {
	parts, err := c.buildCurrentMessageParts(messages, images)
	if err != nil {
		return nil, err
	}

	if len(messages) <= 1 {
		return parts, nil
	}

	historyPrompt := buildHistoryPrompt(messages)
	return append([]genai.Part{genai.Text(historyPrompt)}, parts...), nil
}

func buildHistoryPrompt(messages []model.Message) string {
	var b strings.Builder
	b.WriteString("【過去の会話履歴】\n")
	for i := 0; i < len(messages)-1; i++ {
		role := "ユーザー"
		if messages[i].Role == model.RoleAssistant {
			role = "アシスタント"
		}
		fmt.Fprintf(&b, "%s: %s\n", role, messages[i].Content)
	}
	b.WriteString("【会話履歴終了】\n\n上記の会話履歴を踏まえて、ユーザーの最新の発言に応答してください。\n\n")
	return b.String()
}

func configureModel(requestModel *genai.GenerativeModel, systemPrompt string, maxTokens int64, temperature float64) {
	// システムプロンプトの設定
	if systemPrompt != "" {
		requestModel.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(systemPrompt)},
		}
	} else {
		requestModel.SystemInstruction = nil
	}

	// トークン上限の設定
	if maxTokens > 0 {
		requestModel.SetMaxOutputTokens(int32(maxTokens))
	}
	// Temperatureの設定
	requestModel.SetTemperature(float32(temperature))

	requestModel.SafetySettings = []*genai.SafetySetting{
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

	return "", fmt.Errorf("response too short (%d chars)", runeCount)
}

func (c *Client) IsRetryable(err error) bool {
	if gerr, ok := err.(*googleapi.Error); ok {
		// 429はリトライせず即座に失敗扱い（別途 IsRateLimited で判定）
		return gerr.Code >= http.StatusInternalServerError
	}
	return false
}

func (c *Client) IsBadRequest(err error) bool {
	if gerr, ok := err.(*googleapi.Error); ok {
		return gerr.Code == http.StatusBadRequest
	}
	return false
}

// IsRateLimited はエラーが429 Too Many Requestsかを判定する
func (c *Client) IsRateLimited(err error) bool {
	if gerr, ok := err.(*googleapi.Error); ok {
		return gerr.Code == http.StatusTooManyRequests
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
