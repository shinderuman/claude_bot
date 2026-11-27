package siwc

import (
	"errors"
	"fmt"
)

var (
	ErrReauthRequired            = errors.New("OpenAI再認証が必要です (openai_auth login を実行してください)")
	errAuthorizationCodeRejected = errors.New("認証コードの交換が拒否されました (invalid_grant)")
)

type OAuthError struct {
	StatusCode  int
	Code        string
	Description string
	RequestID   string
}

func (e *OAuthError) Error() string {
	message := "OpenAIトークン取得失敗: " + responseStatus(e.StatusCode, e.RequestID)
	if e.Code != "" {
		message += " " + e.Code
	}
	if e.Description != "" {
		message += ": " + e.Description
	}
	return message
}

func responseStatus(status int, requestID string) string {
	message := fmt.Sprintf("HTTP %d", status)
	if requestID != "" {
		message += " (request_id: " + requestID + ")"
	}
	return message
}

func isUnusableRefreshTokenError(err error) bool {
	var oauthErr *OAuthError
	if !errors.As(err, &oauthErr) {
		return false
	}
	switch oauthErr.Code {
	case "invalid_grant", "invalid_refresh_token", "token_expired",
		"refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return true
	}
	return false
}
