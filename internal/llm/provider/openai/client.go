package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm/provider"
	"claude_bot/internal/model"
	"claude_bot/internal/siwc"
)

const requestTimeout = 60 * time.Second

var errStructuredOutputUnsupported = errors.New("OpenAIの構造化出力は未対応")

type Client struct {
	model      string
	apiBaseURL string
	session    *siwc.Session
	httpClient *http.Client
}

func NewClient(cfg *config.Config) (provider.Provider, error) {
	if cfg.OpenAIModel == "" {
		return nil, errors.New("OpenAIプロバイダーには OPENAI_MODEL が必要です")
	}
	credentialPath, err := siwc.ResolveCredentialPath(cfg.OpenAICredentialFile)
	if err != nil {
		return nil, err
	}
	if _, err := siwc.LoadCredential(credentialPath); err != nil {
		return nil, fmt.Errorf("%w。openai_auth loginで初回認証または再認証を実行してください", err)
	}

	endpoints := siwc.OfficialEndpoints()
	httpClient := siwc.NewHTTPClient(requestTimeout)
	session := siwc.NewSession(credentialPath, endpoints, httpClient)
	return &Client{
		model:      cfg.OpenAIModel,
		apiBaseURL: endpoints.APIBaseURL,
		session:    session,
		httpClient: httpClient,
	}, nil
}

func (c *Client) SupportsStructuredOutput() bool {
	return false
}

func (c *Client) GenerateStructuredContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64, schema *provider.StructuredSchema) (string, string, error) {
	return "", "", errStructuredOutputUnsupported
}

func (c *Client) GenerateContent(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, images []model.Image, temperature float64) (string, string, error) {
	body, err := buildRequestBody(c.model, messages, systemPrompt, images)
	if err != nil {
		return "", "", err
	}
	accessToken, err := c.session.EnsureAccessToken(ctx)
	if err != nil {
		return "", "", err
	}
	content, err := c.postResponses(ctx, accessToken, body)
	if err != nil {
		return "", "", err
	}
	return content, "", nil
}

func (c *Client) IsRetryable(err error) bool {
	var apiErr *ResponsesError
	if errors.As(err, &apiErr) {
		return apiErr.statusForRecovery() >= 500
	}
	return false
}

func (c *Client) IsBadRequest(err error) bool {
	var apiErr *ResponsesError
	if errors.As(err, &apiErr) {
		return apiErr.statusForRecovery() == http.StatusBadRequest
	}
	return false
}

func (c *Client) IsRateLimited(err error) bool {
	var apiErr *ResponsesError
	if errors.As(err, &apiErr) {
		return apiErr.statusForRecovery() == http.StatusTooManyRequests
	}
	return false
}

func (c *Client) postResponses(ctx context.Context, accessToken string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBaseURL+siwc.ResponsesPath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("OpenAI Responses APIへ接続できません: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", responsesErrorFromHTTP(resp)
	}
	content, err := consumeResponseStream(resp.Body)
	if err == nil {
		return content, nil
	}
	var apiErr *ResponsesError
	if !errors.As(err, &apiErr) {
		apiErr = &ResponsesError{cause: err}
	}
	apiErr.StatusCode = resp.StatusCode
	apiErr.RequestID = resp.Header.Get("x-request-id")
	return "", apiErr
}

func responsesErrorFromHTTP(resp *http.Response) error {
	apiErr := &ResponsesError{StatusCode: resp.StatusCode, RequestID: resp.Header.Get("x-request-id")}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		apiErr.cause = fmt.Errorf("応答本体の読込失敗: %w", err)
		return apiErr
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Param   string `json:"param"`
		} `json:"error"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &payload) == nil {
		apiErr.Code = payload.Error.Code
		apiErr.Param = payload.Error.Param
		apiErr.Message = payload.Error.Message
		if apiErr.Message == "" {
			apiErr.Message = payload.Detail
		}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w。選択中のChatGPTアカウントと付与スコープを確認してください。失効している場合は openai_auth login で再認証してください", apiErr)
	}
	return apiErr
}

type inputTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type inputImagePart struct {
	Type     string `json:"type"`
	ImageURL string `json:"image_url"`
}

type inputMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type responsesRequestBody struct {
	Model        string         `json:"model"`
	Input        []inputMessage `json:"input"`
	Instructions string         `json:"instructions,omitempty"`
	Store        bool           `json:"store"`
	Stream       bool           `json:"stream"`
}

func buildRequestBody(modelName string, messages []model.Message, systemPrompt string, images []model.Image) ([]byte, error) {
	request := responsesRequestBody{
		Model:        modelName,
		Input:        convertMessages(messages, images),
		Instructions: systemPrompt,
		Store:        false,
		Stream:       true,
	}
	return json.Marshal(request)
}

func convertMessages(messages []model.Message, currentImages []model.Image) []inputMessage {
	result := make([]inputMessage, len(messages))
	for i, msg := range messages {
		role := model.RoleUser
		if msg.Role == model.RoleAssistant {
			role = model.RoleAssistant
		}
		if i == len(messages)-1 && len(currentImages) > 0 && msg.Role != model.RoleAssistant {
			parts := []any{inputTextPart{Type: "input_text", Text: msg.Content}}
			for _, img := range currentImages {
				parts = append(parts, inputImagePart{
					Type:     "input_image",
					ImageURL: "data:" + img.MediaType + ";base64," + img.Data,
				})
			}
			result[i] = inputMessage{Role: role, Content: parts}
			continue
		}
		result[i] = inputMessage{Role: role, Content: msg.Content}
	}
	return result
}
