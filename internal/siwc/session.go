package siwc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

type tokenResponse struct {
	AccessToken      string  `json:"access_token"`
	RefreshToken     string  `json:"refresh_token"`
	IDToken          string  `json:"id_token"`
	TokenType        string  `json:"token_type"`
	ExpiresIn        int64   `json:"expires_in"`
	Scope            *string `json:"scope"`
	Error            string  `json:"error"`
	ErrorDescription string  `json:"error_description"`
	statusCode       int
	requestID        string
}

func (r tokenResponse) grantedScopes() []string {
	if r.Scope == nil {
		return nil
	}
	return strings.Fields(*r.Scope)
}

type Session struct {
	path       string
	endpoints  Endpoints
	httpClient *http.Client
	now        func() time.Time
}

func NewSession(path string, endpoints Endpoints, httpClient *http.Client) *Session {
	return &Session{
		path:       path,
		endpoints:  endpoints,
		httpClient: httpClient,
		now:        time.Now,
	}
}

func (s *Session) EnsureAccessToken(ctx context.Context) (string, error) {
	credential, err := LoadCredential(s.path)
	if err != nil {
		return "", err
	}
	if !credential.NeedsRefresh(s.now()) {
		return usableAccessToken(credential)
	}

	return s.refreshAccessToken(ctx)
}

func (s *Session) refreshAccessToken(ctx context.Context) (string, error) {
	var token string
	err := withCredentialLock(ctx, s.path, func() error {
		credential, err := LoadCredential(s.path)
		if err != nil {
			return err
		}
		if !credential.NeedsRefresh(s.now()) || !credential.HasPlanUsageScope() {
			token, err = usableAccessToken(credential)
			return err
		}
		if credential.RefreshToken == "" {
			return fmt.Errorf("%w: リフレッシュトークンがありません", ErrReauthRequired)
		}

		response, err := s.refreshToken(ctx, credential)
		if err != nil {
			if isUnusableRefreshTokenError(err) {
				credential.ClearTokens()
				if saveErr := SaveCredential(s.path, credential); saveErr != nil {
					return fmt.Errorf("%w: トークン破棄後の保存に失敗: %v", err, saveErr)
				}
				return fmt.Errorf("%w: %v", ErrReauthRequired, err)
			}
			return err
		}

		applyRefreshedTokens(credential, response, s.now())
		if err := SaveCredential(s.path, credential); err != nil {
			return err
		}
		token, err = usableAccessToken(credential)
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func usableAccessToken(credential *Credential) (string, error) {
	if !credential.HasPlanUsageScope() {
		return "", fmt.Errorf("%w: プラン使用スコープ (%s) が付与されていません", ErrReauthRequired, PlanUsageScope)
	}
	return credential.AccessToken, nil
}

func (s *Session) refreshToken(ctx context.Context, credential *Credential) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", credential.ClientID)
	form.Set("refresh_token", credential.RefreshToken)
	form.Set("resource", s.endpoints.Resource())
	return postTokenRequest(ctx, s.endpoints, s.httpClient, form)
}

func postTokenRequest(ctx context.Context, endpoints Endpoints, httpClient *http.Client, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.TokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenAIトークンエンドポイントへ接続できません: %w", err)
	}
	defer resp.Body.Close()

	requestID := resp.Header.Get("x-request-id")
	var response tokenResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("OpenAIトークン応答読込失敗: %s: %w", responseStatus(resp.StatusCode, requestID), err)
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("OpenAIトークン応答解析失敗: %s: %w", responseStatus(resp.StatusCode, requestID), err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &OAuthError{StatusCode: resp.StatusCode, Code: response.Error, Description: response.ErrorDescription, RequestID: requestID}
	}
	refreshing := form.Get("grant_type") == "refresh_token"
	var invalidField string
	switch {
	case response.AccessToken == "":
		invalidField = "access_tokenがありません"
	case !refreshing && response.IDToken == "":
		invalidField = "id_tokenがありません"
	case response.RefreshToken == "" && (refreshing || slices.Contains(response.grantedScopes(), "offline_access")):
		invalidField = "refresh_tokenがありません"
	case !strings.EqualFold(response.TokenType, "Bearer"):
		invalidField = "token_typeが不正です"
	case response.ExpiresIn <= 0:
		invalidField = "expires_inが不正です"
	}
	if invalidField != "" {
		return nil, fmt.Errorf("OpenAIトークン応答が不正です: %s: %s", responseStatus(resp.StatusCode, requestID), invalidField)
	}
	response.statusCode = resp.StatusCode
	response.requestID = requestID
	return &response, nil
}

func applyRefreshedTokens(credential *Credential, response *tokenResponse, now time.Time) {
	credential.AccessToken = response.AccessToken
	credential.RefreshToken = response.RefreshToken
	if response.IDToken != "" {
		credential.IDToken = response.IDToken
	}
	credential.TokenType = response.TokenType
	credential.ExpiresIn = response.ExpiresIn
	if response.Scope != nil {
		credential.Scopes = response.grantedScopes()
	}
	credential.SavedAt = now.UTC()
}

func withCredentialLock(ctx context.Context, path string, fn func() error) error {
	lock := flock.New(path + ".lock")
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("認証情報ファイルをロックできません (%s): %w", path, err)
	}
	if !locked {
		return fmt.Errorf("認証情報ファイルのロック待機が中断されました (%s)", path)
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}
