package siwc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type ModelInfo struct {
	Slug        string
	DisplayName string
}

type modelsResponse struct {
	Models []struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Visibility  string `json:"visibility"`
	} `json:"models"`
}

func ListModels(ctx context.Context, endpoints Endpoints, httpClient *http.Client, accessToken string) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoints.ModelsURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenAIモデル一覧を取得できません: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, modelListError(resp, fmt.Errorf("応答読込失敗: %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		var reason error
		if message := describeBody(body); message != "" {
			reason = errors.New(message)
		}
		return nil, modelListError(resp, reason)
	}

	var listing modelsResponse
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, modelListError(resp, fmt.Errorf("応答解析失敗: %w", err))
	}
	if listing.Models == nil {
		return nil, modelListError(resp, errors.New("応答に models 配列がありません"))
	}
	var models []ModelInfo
	for _, model := range listing.Models {
		if model.Visibility != "list" {
			continue
		}
		models = append(models, ModelInfo{Slug: model.Slug, DisplayName: model.DisplayName})
	}
	return models, nil
}

func modelListError(resp *http.Response, reason error) error {
	message := "OpenAIモデル一覧取得失敗: " + responseStatus(resp.StatusCode, resp.Header.Get("x-request-id"))
	err := errors.New(message)
	if reason != nil {
		err = fmt.Errorf("%s: %w", message, reason)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %w", err, ErrReauthRequired)
	}
	return err
}

func describeBody(body []byte) string {
	var structured struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &structured); err != nil {
		return string(body)
	}
	message := structured.Error.Message
	if message == "" {
		message = structured.Detail
	}
	if structured.Error.Code == "" {
		return message
	}
	if message == "" {
		return structured.Error.Code
	}
	return structured.Error.Code + ": " + message
}
