package siwc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func newLoginTestSession(t *testing.T) *loginSession {
	t.Helper()
	session, err := newLoginSession("http://127.0.0.1:1455/auth/callback", nil)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func authorizeQuery(t *testing.T, rawURL string) url.Values {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query()
}

func TestBuildAuthorizeURLFirstRegistration(t *testing.T) {
	session := newLoginTestSession(t)

	query := authorizeQuery(t, buildAuthorizeURL(testEndpoints, session, "urn:uuid:host-1"))

	if query.Get("client_id") != DynamicClientID {
		t.Errorf("client_id = %q, want %q", query.Get("client_id"), DynamicClientID)
	}
	if query.Get("agent_name_hint") != AgentNameHint {
		t.Errorf("agent_name_hint = %q, want %q", query.Get("agent_name_hint"), AgentNameHint)
	}
	if query.Get("ext_agent_host_id") != "urn:uuid:host-1" {
		t.Errorf("ext_agent_host_id = %q", query.Get("ext_agent_host_id"))
	}
	if query.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", query.Get("response_type"))
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", query.Get("code_challenge_method"))
	}
	if query.Get("code_challenge") != pkceCodeChallenge(session.codeVerifier) {
		t.Errorf("code_challenge = %q, want the digest of the verifier", query.Get("code_challenge"))
	}
	if query.Get("scope") != RequestScopes {
		t.Errorf("scope = %q, want %q", query.Get("scope"), RequestScopes)
	}
	if query.Get("nonce") != session.nonce || query.Get("state") != session.state {
		t.Error("authorize URL does not carry the fresh state and nonce")
	}
	if query.Get("redirect_uri") != session.redirectURI {
		t.Errorf("redirect_uri = %q, want %q", query.Get("redirect_uri"), session.redirectURI)
	}
	if query.Get("resource") != "https://api.test" {
		t.Errorf("resource = %q", query.Get("resource"))
	}
	if _, hasHint := query["id_token_hint"]; hasHint {
		t.Error("first registration must not send id_token_hint")
	}
}

func TestBuildAuthorizeURLReauthorization(t *testing.T) {
	session, err := newLoginSession("http://127.0.0.1:1455/auth/callback", &Credential{
		ClientID: "oaiapp_saved",
		IDToken:  "id-token-1",
		Email:    "user@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	query := authorizeQuery(t, buildAuthorizeURL(testEndpoints, session, "urn:uuid:host-1"))

	if query.Get("client_id") != "oaiapp_saved" {
		t.Errorf("client_id = %q, want oaiapp_saved", query.Get("client_id"))
	}
	if _, hasHint := query["agent_name_hint"]; hasHint {
		t.Error("reauthorization must omit agent_name_hint")
	}
	if query.Has("id_token_hint") || query.Has("login_hint") {
		t.Error("the URL displayed in the terminal contains account credentials")
	}
	if query.Get("ext_agent_host_id") != "urn:uuid:host-1" {
		t.Errorf("ext_agent_host_id = %q", query.Get("ext_agent_host_id"))
	}
}

func TestParseCallback(t *testing.T) {
	tests := []struct {
		name          string
		savedClientID string
		query         url.Values
		wantCode      string
		wantClientID  string
		wantErr       bool
	}{
		{
			name:         "first registration",
			query:        url.Values{"code": {"auth-code"}, "state": {"state-1"}, "client_id": {"oaiapp_issued"}},
			wantCode:     "auth-code",
			wantClientID: "oaiapp_issued",
		},
		{
			name:    "first registration without issued client id",
			query:   url.Values{"code": {"auth-code"}, "state": {"state-1"}},
			wantErr: true,
		},
		{
			name:    "first registration with dynamic client id",
			query:   url.Values{"code": {"auth-code"}, "state": {"state-1"}, "client_id": {DynamicClientID}},
			wantErr: true,
		},
		{
			name:    "state mismatch",
			query:   url.Values{"code": {"auth-code"}, "state": {"wrong-state"}, "client_id": {"oaiapp_issued"}},
			wantErr: true,
		},
		{
			name:    "access denied",
			query:   url.Values{"error": {"access_denied"}, "state": {"state-1"}},
			wantErr: true,
		},
		{
			name:    "missing code",
			query:   url.Values{"state": {"state-1"}, "client_id": {"oaiapp_issued"}},
			wantErr: true,
		},
		{
			name:          "reauthorization keeps saved client id",
			savedClientID: "oaiapp_saved",
			query:         url.Values{"code": {"auth-code"}, "state": {"state-1"}},
			wantCode:      "auth-code",
			wantClientID:  "oaiapp_saved",
		},
		{
			name:          "reauthorization rejects different client id",
			savedClientID: "oaiapp_saved",
			query:         url.Values{"code": {"auth-code"}, "state": {"state-1"}, "client_id": {"oaiapp_other"}},
			wantErr:       true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := newLoginTestSession(t)
			session.state = "state-1"
			session.savedClientID = test.savedClientID

			callback, err := parseCallback(session, test.query)
			if test.wantErr {
				if err == nil {
					t.Fatal("parseCallback() accepted an invalid callback")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if callback.code != test.wantCode || callback.clientID != test.wantClientID {
				t.Fatalf("callback = %+v, want code %q and client_id %q", callback, test.wantCode, test.wantClientID)
			}
		})
	}
}

func TestCallbackHandlerServesParsedResult(t *testing.T) {
	session := newLoginTestSession(t)
	session.state = "state-1"
	result := make(chan callbackResult, 1)

	handler := (&LoginFlow{}).callbackHandler(session, result)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1455/auth/callback?code=auth-code&state=state-1&client_id=oaiapp_issued", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", recorder.Code)
	}
	select {
	case callback := <-result:
		if callback.code != "auth-code" || callback.clientID != "oaiapp_issued" {
			t.Fatalf("callback result = %+v", callback)
		}
	default:
		t.Fatal("callback result was not delivered")
	}

	unrelated := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1455/favicon.ico", nil)
	unrelatedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unrelatedRecorder, unrelated)
	if unrelatedRecorder.Code != http.StatusNotFound {
		t.Fatalf("unrelated path status = %d, want 404", unrelatedRecorder.Code)
	}
}

func authFlowTransport(t *testing.T, signer *testJWTSigner, tokenBody func() string) roundTripFunc {
	t.Helper()
	return func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == TokenPath {
			form := requestBody(req)
			if form.Get("grant_type") != "authorization_code" {
				t.Errorf("grant_type = %q, want authorization_code", form.Get("grant_type"))
			}
			if form.Get("code_verifier") == "" || form.Get("code") == "" || form.Get("client_id") == "" {
				t.Errorf("token exchange is missing required fields: %v", form)
			}
			return jsonHTTPResponse(req, 200, tokenBody()), nil
		}
		return jwksTransport(signer)(req)
	}
}

func tokenBodyWithIDToken(idToken string) string {
	response := map[string]any{
		"access_token":  "access-1",
		"refresh_token": "refresh-1",
		"id_token":      idToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"scope":         RequestScopes,
	}
	data, _ := json.Marshal(response)
	return string(data)
}

func TestCompleteLoginStoresVerifiedCredential(t *testing.T) {
	signer := newTestJWTSigner(t)
	idToken := signedTestIDToken(signer, "oaiapp_issued", "nonce-1", fixedNow().Add(time.Hour))
	flow := NewLoginFlow(testEndpoints, stubHTTPClient(authFlowTransport(t, signer, func() string {
		return tokenBodyWithIDToken(idToken)
	})), 1455)
	flow.now = fixedNow

	session := newLoginTestSession(t)
	session.state = "state-1"
	session.nonce = "nonce-1"
	session.codeVerifier = "verifier-1"
	session.redirectURI = "http://127.0.0.1:1455/auth/callback"

	credential, err := flow.completeLogin(t.Context(), session, callbackResult{code: "auth-code", clientID: "oaiapp_issued"}, "urn:uuid:host-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Email != "user@example.com" || credential.Subject != "subject-1" {
		t.Fatalf("credential identity = %+v", credential)
	}
	if credential.ClientID != "oaiapp_issued" || credential.ExtAgentHostID != "urn:uuid:host-1" {
		t.Fatalf("credential registration = %+v", credential)
	}
	if credential.AccessToken != "access-1" || credential.RefreshToken != "refresh-1" || credential.IDToken != idToken {
		t.Fatalf("credential tokens = %+v", credential)
	}
	if !credential.HasPlanUsageScope() || credential.SavedAt != fixedNow() {
		t.Fatalf("credential scopes/saved_at = %+v", credential)
	}
}

func TestCompleteLoginRejectsAccountMismatchOnReauthorization(t *testing.T) {
	signer := newTestJWTSigner(t)
	idToken := signedTestIDToken(signer, "oaiapp_issued", "nonce-1", fixedNow().Add(time.Hour))
	flow := NewLoginFlow(testEndpoints, stubHTTPClient(authFlowTransport(t, signer, func() string {
		return tokenBodyWithIDToken(idToken)
	})), 1455)
	flow.now = fixedNow
	session := newLoginTestSession(t)
	session.nonce = "nonce-1"
	saved := &Credential{ClientID: "oaiapp_issued", Subject: "another-subject"}

	if _, err := flow.completeLogin(t.Context(), session, callbackResult{code: "auth-code", clientID: "oaiapp_issued"}, "urn:uuid:host-1", saved); err == nil {
		t.Fatal("completeLogin() accepted a different account for a saved registration")
	}
}

func TestSignInReusesRegistrationAfterPlanUsageIsDeclined(t *testing.T) {
	signer := newTestJWTSigner(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	const clientID = "oaiapp_issued"
	var nonce string
	var consentGranted bool
	flow := NewLoginFlow(testEndpoints, stubHTTPClient(authFlowTransport(t, signer, func() string {
		idToken := signedTestIDToken(signer, clientID, nonce, fixedNow().Add(time.Hour))
		if consentGranted {
			return tokenBodyWithIDToken(idToken)
		}
		return `{"access_token":"access-1","id_token":"` + idToken + `","token_type":"Bearer","expires_in":3600,"scope":"openid profile email"}`
	})), 1455)
	flow.now = fixedNow
	signIn := func(wantClientID, wantPrompt string) (*Credential, error) {
		await := func(_ context.Context, session *loginSession, authorizeURL string, _ func(string)) (callbackResult, error) {
			query := authorizeQuery(t, authorizeURL)
			if query.Get("client_id") != wantClientID || query.Get("prompt") != wantPrompt || query.Get("scope") != RequestScopes {
				t.Fatalf("unexpected OAuth request: client_id=%q prompt=%q scope=%q", query.Get("client_id"), query.Get("prompt"), query.Get("scope"))
			}
			nonce = session.nonce
			return parseCallback(session, url.Values{"code": {"auth-code"}, "state": {session.state}, "client_id": {clientID}})
		}
		return flow.signIn(t.Context(), path, func(string) {}, await)
	}
	if _, err := signIn(DynamicClientID, ""); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("declined sign-in error = %v, want the plan usage guidance", err)
	}

	saved, err := LoadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ClientID != clientID || saved.Subject != "subject-1" || saved.RefreshToken != "" || saved.HasPlanUsageScope() {
		t.Fatal("declined sign-in did not keep the verified registration with plan usage disabled")
	}
	if token, err := NewSession(path, testEndpoints, flow.httpClient).EnsureAccessToken(t.Context()); token != "" || !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("declined sign-in allowed plan access or did not request reauthorization: %v", err)
	}

	consentGranted = true
	credential, err := signIn(clientID, "consent")
	if err != nil {
		t.Fatal(err)
	}
	if !credential.HasPlanUsageScope() || credential.RefreshToken == "" {
		t.Fatal("reauthorization did not enable plan usage")
	}
}

func TestSignInReportsInvalidTokenResponse(t *testing.T) {
	signer := newTestJWTSigner(t)
	const clientID = "oaiapp_issued"
	invalidNonceToken := signedTestIDToken(signer, clientID, "wrong-nonce", fixedNow().Add(time.Hour))
	for _, test := range []struct {
		name                 string
		body                 string
		want                 string
		discoveryUnavailable bool
	}{
		{name: "missing ID token", body: `{"access_token":"access-1","token_type":"Bearer","expires_in":3600,"scope":"openid profile email"}`, want: "id_tokenがありません"},
		{name: "missing refresh token", body: `{"access_token":"access-1","id_token":"id-1","token_type":"Bearer","expires_in":3600,"scope":"openid offline_access"}`, want: "refresh_tokenがありません"},
		{name: "malformed ID token", body: tokenBodyWithIDToken("not-a-jwt"), want: "IDトークンの形式が不正です"},
		{name: "invalid nonce", body: tokenBodyWithIDToken(invalidNonceToken), want: "IDトークンのnonceが一致しません"},
		{name: "discovery unavailable", body: tokenBodyWithIDToken(invalidNonceToken), want: "HTTP 503 (request_id: req-discovery)", discoveryUnavailable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == TokenPath {
					resp := jsonHTTPResponse(req, http.StatusOK, test.body)
					resp.Header.Set("x-request-id", "req-login")
					return resp, nil
				}
				if req.URL.Path == DiscoveryPath && test.discoveryUnavailable {
					resp := jsonHTTPResponse(req, http.StatusServiceUnavailable, "{}")
					resp.Header.Set("x-request-id", "req-discovery")
					return resp, nil
				}
				return jwksTransport(signer)(req)
			})
			flow := NewLoginFlow(testEndpoints, stubHTTPClient(transport), DefaultPort)
			flow.now = fixedNow
			path := filepath.Join(t.TempDir(), "credentials.json")
			await := func(_ context.Context, session *loginSession, _ string, _ func(string)) (callbackResult, error) {
				return parseCallback(session, url.Values{"code": {"auth-code"}, "state": {session.state}, "client_id": {clientID}})
			}

			_, err := flow.signIn(t.Context(), path, func(string) {}, await)
			if err == nil || !strings.Contains(err.Error(), "HTTP 200 (request_id: req-login)") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("signIn() error = %v, want token exchange status and request ID and %q", err, test.want)
			}
			if strings.Contains(err.Error(), "not-a-jwt") || strings.Contains(err.Error(), invalidNonceToken) {
				t.Fatal("signIn() error contains an ID token")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid token response left a credential file: %v", err)
			}
		})
	}
}

func TestSignInRestartsAuthorizationOnceWhenCodeExchangeIsRejected(t *testing.T) {
	tests := []struct {
		name           string
		saved          *Credential
		exchangeErrors []string
		wantAttempts   int
		wantClientID   string
		wantErr        string
	}{
		{
			name:           "first registration keeps the issued client id",
			exchangeErrors: []string{"invalid_grant", ""},
			wantAttempts:   2,
			wantClientID:   "oaiapp_issued",
		},
		{
			name:           "reauthorization keeps the saved registration",
			saved:          &Credential{ClientID: "oaiapp_saved", Subject: "subject-1", IDToken: "id-token-1", Email: "user@example.com", ExtAgentHostID: "urn:uuid:host-1"},
			exchangeErrors: []string{"invalid_grant", ""},
			wantAttempts:   2,
			wantClientID:   "oaiapp_saved",
		},
		{
			name:           "rejected again",
			exchangeErrors: []string{"invalid_grant", "invalid_grant"},
			wantAttempts:   2,
			wantClientID:   "oaiapp_issued",
			wantErr:        "再度openai_auth login",
		},
		{
			name:           "other oauth error is not retried",
			exchangeErrors: []string{"invalid_client"},
			wantAttempts:   1,
			wantClientID:   "oaiapp_issued",
			wantErr:        "再度openai_auth login",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credentials.json")
			if test.saved != nil {
				if err := SaveCredential(path, test.saved); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(HostIDPath(path), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			signer := newTestJWTSigner(t)
			exchanges := 0
			nonce := ""
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != TokenPath {
					return jwksTransport(signer)(req)
				}
				rejection := ""
				if exchanges < len(test.exchangeErrors) {
					rejection = test.exchangeErrors[exchanges]
				}
				exchanges++
				if rejection != "" {
					return jsonHTTPResponse(req, http.StatusBadRequest, `{"error":"`+rejection+`","error_description":"code was rejected"}`), nil
				}
				idToken := signedTestIDToken(signer, requestBody(req).Get("client_id"), nonce, fixedNow().Add(time.Hour))
				return jsonHTTPResponse(req, 200, tokenBodyWithIDToken(idToken)), nil
			})
			flow := NewLoginFlow(testEndpoints, stubHTTPClient(transport), 1455)
			flow.now = fixedNow

			var authorizeQueries []url.Values
			await := func(_ context.Context, session *loginSession, authorizeURL string, notify func(string)) (callbackResult, error) {
				notify(authorizeURL)
				authorizeQueries = append(authorizeQueries, authorizeQuery(t, authorizeURL))
				nonce = session.nonce
				return parseCallback(session, url.Values{"code": {"auth-code"}, "state": {session.state}, "client_id": {test.wantClientID}})
			}

			_, err := flow.signIn(t.Context(), path, func(string) {}, await)
			if len(authorizeQueries) != test.wantAttempts {
				t.Fatalf("authorizations = %d, want %d", len(authorizeQueries), test.wantAttempts)
			}
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("signIn() error = %v, want %q", err, test.wantErr)
				}
				saved, loadErr := LoadCredential(path)
				if test.saved == nil && !errors.Is(loadErr, os.ErrNotExist) {
					t.Fatalf("failed sign-in left a credential behind: %+v (%v)", saved, loadErr)
				}
				if test.saved != nil && (loadErr != nil || saved.RefreshToken != "") {
					t.Fatalf("failed reauthorization changed the saved registration: %+v (%v)", saved, loadErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			first, restarted := authorizeQueries[0], authorizeQueries[1]
			wantFirstClientID := DynamicClientID
			if test.saved != nil {
				wantFirstClientID = test.saved.ClientID
				for _, query := range authorizeQueries {
					if query.Get("ext_agent_host_id") != test.saved.ExtAgentHostID {
						t.Fatal("reauthorization did not use the saved host ID")
					}
				}
			}
			if first.Get("client_id") != wantFirstClientID || restarted.Get("client_id") != test.wantClientID {
				t.Fatalf("client_id = %q then %q", first.Get("client_id"), restarted.Get("client_id"))
			}
			if _, hasHint := restarted["agent_name_hint"]; hasHint {
				t.Error("restarted authorization must omit agent_name_hint")
			}
			for _, param := range []string{"state", "nonce", "code_challenge"} {
				if restarted.Get(param) == first.Get(param) {
					t.Errorf("restarted authorization reused %s", param)
				}
			}
			saved, err := LoadCredential(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.ClientID != test.wantClientID || saved.AccessToken != "access-1" || saved.RefreshToken != "refresh-1" {
				t.Fatalf("saved credential = %+v", saved)
			}
		})
	}
}

func TestSignInPreservesRegistrationSavedDuringAuthorization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	signer := newTestJWTSigner(t)
	signIn := func(subject, clientID string, beforeCallback func()) (*Credential, error) {
		nonce := ""
		transport := authFlowTransport(t, signer, func() string {
			idToken := signer.sign(map[string]any{
				"iss": "https://auth.test", "sub": subject, "aud": clientID,
				"nonce": nonce, "exp": fixedNow().Add(time.Hour).Unix(),
			}, signer.kid)
			return tokenBodyWithIDToken(idToken)
		})
		flow := NewLoginFlow(testEndpoints, stubHTTPClient(transport), DefaultPort)
		flow.now = fixedNow
		await := func(_ context.Context, session *loginSession, _ string, _ func(string)) (callbackResult, error) {
			if session.savedClientID != "" {
				t.Fatal("test requires two first-time sign-ins")
			}
			nonce = session.nonce
			beforeCallback()
			return parseCallback(session, url.Values{"code": {"auth-code"}, "state": {session.state}, "client_id": {clientID}})
		}
		return flow.signIn(t.Context(), path, func(string) {}, await)
	}

	later, err := signIn("second-user", "oaiapp_second", func() {
		if _, err := signIn("first-user", "oaiapp_first", func() {}); err != nil {
			t.Fatal(err)
		}
	})
	if later != nil || err == nil || !strings.Contains(err.Error(), "保存済み") {
		t.Fatalf("later sign-in returned %v, want a registration conflict", err)
	}
	saved, err := LoadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ClientID != "oaiapp_first" || saved.Subject != "first-user" {
		t.Fatalf("saved account changed to %q / %q", saved.ClientID, saved.Subject)
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("ループバックリスナーを利用できません: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestLoginFlowRunFailsWhenContextCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	port := freeLoopbackPort(t)
	flow := NewLoginFlow(testEndpoints, stubHTTPClient(jwksTransport(newTestJWTSigner(t))), port)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := flow.Run(ctx, path, func(string) {}); err == nil {
		t.Fatal("Run() succeeded with a canceled context")
	}
}

func TestLoginFlowRunLoopbackFlowEndToEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	signer := newTestJWTSigner(t)
	port := freeLoopbackPort(t)

	var nonceMu sync.Mutex
	nonce := ""
	setNonce := func(value string) {
		nonceMu.Lock()
		defer nonceMu.Unlock()
		nonce = value
	}
	currentNonce := func() string {
		nonceMu.Lock()
		defer nonceMu.Unlock()
		return nonce
	}

	redirectURI := "http://127.0.0.1:" + strconv.Itoa(port) + CallbackPath
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == TokenPath {
			form := requestBody(req)
			if form.Get("code_verifier") == "" {
				t.Error("token exchange is missing code_verifier")
			}
			if form.Get("redirect_uri") != redirectURI {
				t.Errorf("redirect_uri = %q, want %q", form.Get("redirect_uri"), redirectURI)
			}
			if form.Get("resource") != "https://api.test" {
				t.Errorf("resource = %q, want https://api.test", form.Get("resource"))
			}
			idToken := signedTestIDToken(signer, "oaiapp_issued", currentNonce(), fixedNow().Add(time.Hour))
			return jsonHTTPResponse(req, 200, tokenBodyWithIDToken(idToken)), nil
		}
		return jwksTransport(signer)(req)
	})

	flow := NewLoginFlow(testEndpoints, stubHTTPClient(transport), port)
	flow.now = fixedNow

	notifyDone := make(chan string, 1)
	runDone := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		defer close(runDone)
		credential, err := flow.Run(ctx, path, func(authorizeURL string) {
			notifyDone <- authorizeURL
		})
		if err != nil {
			t.Errorf("Run() error = %v", err)
			return
		}
		if credential.ClientID != "oaiapp_issued" || credential.AccessToken != "access-1" {
			t.Errorf("credential = %+v", credential)
		}
	}()

	var authorizeURL string
	select {
	case authorizeURL = <-notifyDone:
	case <-runDone:
		t.Fatal("Run() finished before producing the authorize URL")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	query := authorizeQuery(t, authorizeURL)
	setNonce(query.Get("nonce"))

	invalidCallback, err := http.Get(redirectURI + "?error=access_denied&state=wrong-state")
	if err != nil {
		t.Fatal(err)
	}
	invalidCallback.Body.Close()
	if invalidCallback.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid callback status = %d, want 400", invalidCallback.StatusCode)
	}

	callbackURL := redirectURI + "?code=auth-code&state=" + url.QueryEscape(query.Get("state")) + "&client_id=oaiapp_issued"
	response, err := http.Get(callbackURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", response.StatusCode)
	}

	select {
	case <-runDone:
	case <-ctx.Done():
		t.Fatal("flow did not finish after the callback")
	}
}
