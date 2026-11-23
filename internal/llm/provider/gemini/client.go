package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const MinProfileResponseLength = 50

type Client struct {
	client *genai.Client
	model  *genai.GenerativeModel
	config *config.Config
}

func NewClient(cfg *config.Config) (provider.Provider, error) {
	ctx := context.Background()

	client, err := genai.NewClient(ctx,
		option.WithAPIKey(cfg.GeminiAPIKey),
	)
	if err != nil {
		return nil, fmt.Errorf("Geminiクライアント作成エラー: %w", err)
	}

	modelName := cfg.GeminiModel
	model := client.GenerativeModel(modelName)

	return &Client{
		client: client,
		model:  model,
		config: cfg,
	}, nil
}

func (c *Client) GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error) {
	requestModel := c.requestModel(systemPrompt, maxTokens, temperature)

	parts, err := c.buildRequestParts(messages, images)
	if err != nil {
		return "", "", err
	}

	resp, err := requestModel.GenerateContent(ctx, parts...)
	if err != nil {
		return "", "", err
	}
	response, err := c.validateResponse(ctx, resp)
	return response, "", err
}

func (c *Client) requestModel(systemPrompt string, maxTokens int64, temperature float64) *genai.GenerativeModel {
	requestModel := *c.model
	configureModel(&requestModel, systemPrompt, maxTokens, temperature)
	return &requestModel
}

// ChatSession.SendMessage uses a streaming API whose responses failed to parse with these models.
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
	if systemPrompt != "" {
		requestModel.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(systemPrompt)},
		}
	} else {
		requestModel.SystemInstruction = nil
	}

	if maxTokens > 0 {
		requestModel.SetMaxOutputTokens(int32(maxTokens))
	}
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
	if len(messages) == 0 {
		return nil, fmt.Errorf("メッセージが空です")
	}

	lastMsg := messages[len(messages)-1]

	var parts []genai.Part
	parts = append(parts, genai.Text(lastMsg.Content))

	if len(images) > 0 {
		for _, img := range images {
			data, err := base64.StdEncoding.DecodeString(img.Data)
			if err != nil {
				log.Printf("画像デコード警告: %v", err)
				continue
			}

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

	return "", fmt.Errorf("%w (%d chars, reason: %s)", provider.ErrResponseTooShort, runeCount, finishReason)
}

func (c *Client) IsRetryable(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code >= http.StatusInternalServerError
	}
	return false
}

func (c *Client) IsBadRequest(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusBadRequest
	}
	return false
}

func (c *Client) IsRateLimited(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusTooManyRequests
	}
	return false
}

func extractResponseText(resp *genai.GenerateContentResponse) string {
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
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
