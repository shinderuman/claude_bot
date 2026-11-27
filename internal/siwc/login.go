package siwc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var errCallbackState = errors.New("認可コールバックのstateが一致しません")

type loginSession struct {
	state          string
	nonce          string
	codeVerifier   string
	codeChallenge  string
	redirectURI    string
	savedClientID  string
	requestConsent bool
}

type callbackResult struct {
	code     string
	clientID string
	err      error
}

type callbackAwaiter func(ctx context.Context, session *loginSession, authorizeURL string, notify func(authorizeURL string)) (callbackResult, error)

type LoginFlow struct {
	endpoints  Endpoints
	httpClient *http.Client
	port       int
	now        func() time.Time
}

func NewLoginFlow(endpoints Endpoints, httpClient *http.Client, port int) *LoginFlow {
	return &LoginFlow{
		endpoints:  endpoints,
		httpClient: httpClient,
		port:       port,
		now:        time.Now,
	}
}

func (f *LoginFlow) Run(ctx context.Context, credentialPath string, notify func(authorizeURL string)) (*Credential, error) {
	return f.signIn(ctx, credentialPath, notify, f.awaitLoopbackCallback)
}

func (f *LoginFlow) signIn(ctx context.Context, credentialPath string, notify func(authorizeURL string), await callbackAwaiter) (*Credential, error) {
	credentialPath, err := ResolveCredentialPath(credentialPath)
	if err != nil {
		return nil, err
	}
	var hostID string
	var saved *Credential
	err = withCredentialLock(ctx, credentialPath, func() error {
		var err error
		saved, err = loadExistingRegistration(credentialPath)
		if err != nil {
			return err
		}
		if saved != nil {
			hostID = saved.ExtAgentHostID
			if strings.TrimSpace(hostID) == "" {
				return errors.New("保存済みの認証情報にホストIDがありません")
			}
			return nil
		}
		hostID, err = LoadOrCreateHostID(credentialPath)
		return err
	})
	if err != nil {
		return nil, err
	}

	redirectURI := "http://" + net.JoinHostPort(LoopbackHost, strconv.Itoa(f.port)) + CallbackPath
	registration := saved
	for attempt := 0; ; attempt++ {
		session, err := newLoginSession(redirectURI, registration)
		if err != nil {
			return nil, err
		}
		callback, err := await(ctx, session, buildAuthorizeURL(f.endpoints, session, hostID), notify)
		if err != nil {
			return nil, err
		}
		credential, err := f.completeLogin(ctx, session, callback, hostID, saved)
		if err == nil {
			return f.storeLogin(ctx, credentialPath, credential)
		}
		if !errors.Is(err, errAuthorizationCodeRejected) {
			return nil, err
		}
		if attempt > 0 {
			return nil, fmt.Errorf("%w。再度openai_auth loginを実行してください", err)
		}
		if registration == nil {
			registration = &Credential{ClientID: callback.clientID}
		}
	}
}

func (f *LoginFlow) awaitLoopbackCallback(ctx context.Context, session *loginSession, authorizeURL string, notify func(string)) (callbackResult, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(LoopbackHost, strconv.Itoa(f.port)))
	if err != nil {
		return callbackResult{}, fmt.Errorf("ループバックリスナーを開始できません (127.0.0.1:%d): %w", f.port, err)
	}
	result := make(chan callbackResult, 1)
	server := &http.Server{
		Handler:           f.callbackHandler(session, result),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
	}()

	notify(authorizeURL)

	select {
	case callback := <-result:
		if callback.err != nil {
			return callbackResult{}, callback.err
		}
		return callback, nil
	case <-ctx.Done():
		return callbackResult{}, fmt.Errorf("ログイン待機が中断されました: %w", ctx.Err())
	}
}

func (f *LoginFlow) storeLogin(ctx context.Context, credentialPath string, credential *Credential) (*Credential, error) {
	err := withCredentialLock(ctx, credentialPath, func() error {
		current, err := loadExistingRegistration(credentialPath)
		if err != nil {
			return err
		}
		if current != nil && (current.Subject != credential.Subject ||
			current.ClientID != credential.ClientID || current.ExtAgentHostID != credential.ExtAgentHostID) {
			return errors.New("保存済みのChatGPTアカウントまたは登録と一致しません")
		}
		return SaveCredential(credentialPath, credential)
	})
	if err != nil {
		return nil, err
	}
	if !credential.HasPlanUsageScope() {
		return nil, fmt.Errorf("%w: ChatGPTプランの利用を許可してください", ErrReauthRequired)
	}
	return credential, nil
}

func (f *LoginFlow) completeLogin(ctx context.Context, session *loginSession, callback callbackResult, hostID string, saved *Credential) (*Credential, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", callback.clientID)
	form.Set("code", callback.code)
	form.Set("code_verifier", session.codeVerifier)
	form.Set("redirect_uri", session.redirectURI)
	form.Set("resource", f.endpoints.Resource())

	response, err := postTokenRequest(ctx, f.endpoints, f.httpClient, form)
	if err != nil {
		return nil, authorizationCodeError(err)
	}

	claims, err := verifyIDToken(ctx, f.endpoints, f.httpClient, response.IDToken, callback.clientID, session.nonce, f.now())
	if err != nil {
		return nil, fmt.Errorf("OpenAIトークン交換: %s: %w", responseStatus(response.statusCode, response.requestID), err)
	}
	if saved != nil && saved.Subject != "" && saved.Subject != claims.Sub {
		return nil, errors.New("保存済みのChatGPTアカウントと一致しません")
	}

	return &Credential{
		Email:          claims.Email,
		Issuer:         claims.Iss,
		Subject:        claims.Sub,
		ClientID:       callback.clientID,
		ExtAgentHostID: hostID,
		IDToken:        response.IDToken,
		AccessToken:    response.AccessToken,
		RefreshToken:   response.RefreshToken,
		TokenType:      response.TokenType,
		ExpiresIn:      response.ExpiresIn,
		Scopes:         response.grantedScopes(),
		SavedAt:        f.now().UTC(),
	}, nil
}

func authorizationCodeError(err error) error {
	var oauthErr *OAuthError
	if !errors.As(err, &oauthErr) {
		return err
	}
	if oauthErr.Code == "invalid_grant" {
		return fmt.Errorf("%w: %v", errAuthorizationCodeRejected, oauthErr)
	}
	return fmt.Errorf("認証コード交換に失敗しました (%v)。再度openai_auth loginを実行してください", oauthErr)
}

func (f *LoginFlow) callbackHandler(session *loginSession, result chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != CallbackPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		callback, err := parseCallback(session, r.URL.Query())
		if errors.Is(err, errCallbackState) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			select {
			case result <- callbackResult{err: err}:
			default:
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case result <- callback:
		default:
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("認証コードを受け付けました。ターミナルで結果を確認してください。"))
	})
}

func parseCallback(session *loginSession, query url.Values) (callbackResult, error) {
	if query.Get("state") != session.state {
		return callbackResult{}, errCallbackState
	}
	if errorValue := query.Get("error"); errorValue != "" {
		return callbackResult{}, fmt.Errorf("OpenAI認可失敗: %s", errorValue)
	}
	if query.Get("code") == "" {
		return callbackResult{}, errors.New("認証コードがコールバックに含まれていません")
	}

	issuedClientID := query.Get("client_id")
	if session.savedClientID == "" {
		if issuedClientID == "" || issuedClientID == DynamicClientID {
			return callbackResult{}, errors.New("発行されたclient_idがコールバックに含まれていません")
		}
		return callbackResult{code: query.Get("code"), clientID: issuedClientID}, nil
	}
	if issuedClientID != "" && issuedClientID != session.savedClientID {
		return callbackResult{}, fmt.Errorf("発行されたclient_idが保存済みの登録と一致しません: %s", issuedClientID)
	}
	return callbackResult{code: query.Get("code"), clientID: session.savedClientID}, nil
}

func loadExistingRegistration(credentialPath string) (*Credential, error) {
	credential, err := LoadCredential(credentialPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return credential, nil
}

func newLoginSession(redirectURI string, saved *Credential) (*loginSession, error) {
	state, err := randomToken()
	if err != nil {
		return nil, err
	}
	nonce, err := randomToken()
	if err != nil {
		return nil, err
	}
	codeVerifier, err := randomToken()
	if err != nil {
		return nil, err
	}
	session := &loginSession{
		state:         state,
		nonce:         nonce,
		codeVerifier:  codeVerifier,
		codeChallenge: pkceCodeChallenge(codeVerifier),
		redirectURI:   redirectURI,
	}
	if saved != nil {
		session.savedClientID = saved.ClientID
		session.requestConsent = saved.Subject != "" && !saved.HasPlanUsageScope()
	}
	return session, nil
}

func randomToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("乱数生成に失敗しました: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func pkceCodeChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func buildAuthorizeURL(endpoints Endpoints, session *loginSession, hostID string) string {
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("redirect_uri", session.redirectURI)
	query.Set("scope", RequestScopes)
	query.Set("state", session.state)
	query.Set("nonce", session.nonce)
	query.Set("code_challenge", session.codeChallenge)
	query.Set("code_challenge_method", "S256")
	query.Set("resource", endpoints.Resource())
	query.Set("ext_agent_host_id", hostID)
	if session.savedClientID == "" {
		query.Set("client_id", DynamicClientID)
		query.Set("agent_name_hint", AgentNameHint)
	} else {
		query.Set("client_id", session.savedClientID)
		if session.requestConsent {
			query.Set("prompt", "consent")
		}
	}
	return endpoints.AuthorizeURL() + "?" + query.Encode()
}
