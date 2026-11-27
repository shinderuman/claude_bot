package openaitool

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"claude_bot/internal/siwc"
)

func TestRunRejectsUnknownCommands(t *testing.T) {
	if err := Run(nil); err == nil {
		t.Fatal("Run() without a subcommand succeeded")
	}
	if err := Run([]string{"logout"}); err == nil || !strings.Contains(err.Error(), "不明なコマンド") {
		t.Fatalf("Run(logout) error = %v", err)
	}
}

func TestRunModelsRequiresCredentialPath(t *testing.T) {
	if err := Run([]string{"models"}); err == nil || !strings.Contains(err.Error(), "OPENAI_CREDENTIAL_FILE") {
		t.Fatalf("Run(models) error = %v", err)
	}
}

func TestRunLoginRejectsDeploymentDataPaths(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "checkout")
	dataDir := filepath.Join(project, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "deploy.sh"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module claude_bot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(dataDir, alias); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{dataDir, alias} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			path := filepath.Join(dir, "credentials.json")
			if err := Run([]string{"login", "-credential", path}); err == nil || !strings.Contains(err.Error(), "data/ の外") {
				t.Fatalf("login error = %v, want rejection before authorization", err)
			}
		})
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("login created files under the deployment directory: %v", entries)
	}
}

func TestRunModelsAcceptsUnrelatedDataDirectoryFromDifferentWorkingDirs(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "deploy.sh"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "go.mod"), []byte("module unrelated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "credentials.json")
	if err := siwc.SaveCredential(path, &siwc.Credential{
		ClientID: "oaiapp_test", AccessToken: "access", TokenType: "Bearer",
		ExpiresIn: 3600, SavedAt: time.Now(), Scopes: strings.Fields(siwc.RequestScopes),
	}); err != nil {
		t.Fatal(err)
	}
	stubOpenAITransport(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != siwc.OfficialEndpoints().ModelsURL() || req.Header.Get("Authorization") != "Bearer access" {
			t.Error("model lookup did not use the saved OAuth credential")
		}
		return stubJSON(req, http.StatusOK, map[string]any{"models": []any{}}), nil
	})
	for _, cwd := range []string{base, t.TempDir()} {
		t.Run(filepath.Base(cwd), func(t *testing.T) {
			t.Chdir(cwd)
			if err := Run([]string{"models", "-credential", path}); err != nil {
				t.Fatalf("models error from %s: %v", cwd, err)
			}
		})
	}
}

func requireLoopbackPort(t *testing.T, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(siwc.LoopbackHost, strconv.Itoa(port)))
	if err != nil {
		t.Skipf("ループバックポート %d を利用できません: %v", port, err)
	}
	listener.Close()
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubOpenAITransport(t *testing.T, handler roundTripFunc) http.RoundTripper {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = handler
	t.Cleanup(func() { http.DefaultTransport = previous })
	return previous
}

func stubJSON(req *http.Request, status int, body any) *http.Response {
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(data)),
		Request:    req,
	}
}

func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	logged := &syncBuffer{}
	previous := log.Writer()
	log.SetOutput(logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	return logged
}

func expiredCredential(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := siwc.SaveCredential(path, &siwc.Credential{
		Email:        "user@example.com",
		Subject:      "subject-1",
		ClientID:     "oaiapp_test",
		AccessToken:  "expired-access",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Scopes:       strings.Fields(siwc.RequestScopes),
		SavedAt:      time.Now().UTC().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunModelsListsSlugsAfterRefresh(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://other.invalid")
	t.Setenv("OPENAI_AUTH_BASE_URL", "https://other.invalid")
	credentialPath := expiredCredential(t)
	endpoints := siwc.OfficialEndpoints()
	var refreshes int
	stubOpenAITransport(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case endpoints.TokenURL():
			refreshes++
			return stubJSON(req, http.StatusOK, map[string]any{
				"access_token":  "access-2",
				"refresh_token": "refresh-2",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"scope":         siwc.RequestScopes,
			}), nil
		case endpoints.ModelsURL():
			if req.Header.Get("Authorization") != "Bearer access-2" {
				t.Errorf("models authorization = %q", req.Header.Get("Authorization"))
			}
			return stubJSON(req, http.StatusOK, map[string]any{"models": []map[string]string{
				{"slug": "gpt-listed", "display_name": "Listed Model", "visibility": "list"},
				{"slug": "gpt-hidden", "display_name": "Hidden Model", "visibility": "hidden"},
			}}), nil
		}
		return stubJSON(req, http.StatusNotFound, map[string]any{}), nil
	})
	logged := captureLog(t)

	if err := Run([]string{"models", "-credential", credentialPath}); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh requests = %d, want 1", refreshes)
	}
	if !strings.Contains(logged.String(), "gpt-listed") || strings.Contains(logged.String(), "gpt-hidden") {
		t.Fatalf("model listing output = %q", logged.String())
	}
}

func TestRunModelsReportsAPIFailureDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := siwc.SaveCredential(path, &siwc.Credential{
		ClientID: "oaiapp_test", AccessToken: "access", TokenType: "Bearer",
		ExpiresIn: 3600, SavedAt: time.Now(), Scopes: strings.Fields(siwc.RequestScopes),
	}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   []string
	}{
		{
			name: "401 with reauthorization guidance", status: http.StatusUnauthorized,
			body: `{"error":{"code":"subscription_sharing_invalid_user","message":"account was disconnected"}}`,
			want: []string{"HTTP 401", "subscription_sharing_invalid_user", "account was disconnected", "openai_auth login"},
		},
		{
			name: "message without error code", status: http.StatusForbidden,
			body: `{"error":{"message":"model catalog unavailable"}}`,
			want: []string{"HTTP 403", "model catalog unavailable"},
		},
		{
			name: "direct admission detail", status: http.StatusServiceUnavailable,
			body: `{"detail":"direct routing unavailable"}`,
			want: []string{"HTTP 503", "direct routing unavailable"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubOpenAITransport(t, func(req *http.Request) (*http.Response, error) {
				resp := stubJSON(req, tc.status, json.RawMessage(tc.body))
				resp.Header.Set("x-request-id", "req-models")
				return resp, nil
			})
			err := Run([]string{"models", "-credential", path})
			if err == nil {
				t.Fatal("models succeeded for a failed API response")
			}
			if errors.Is(err, siwc.ErrReauthRequired) != (tc.status == http.StatusUnauthorized) {
				t.Errorf("unexpected reauthorization classification: %v", err)
			}
			for _, want := range append(tc.want, "req-models") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("models error %q does not include %q", err, want)
				}
			}
		})
	}
}

func TestRunLoginCompletesLoopbackFlow(t *testing.T) {
	requireLoopbackPort(t, siwc.DefaultPort)
	credentialPath := filepath.Join(t.TempDir(), "credentials.json")

	signer := newCLITestSigner(t)
	var nonceMu sync.Mutex
	nonce := ""
	setNonce := func(value string) {
		nonceMu.Lock()
		defer nonceMu.Unlock()
		nonce = value
	}

	endpoints := siwc.OfficialEndpoints()
	loopbackTransport := stubOpenAITransport(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case endpoints.DiscoveryURL():
			return stubJSON(req, http.StatusOK, map[string]string{
				"issuer":   endpoints.Issuer(),
				"jwks_uri": endpoints.AuthBaseURL + "/jwks",
			}), nil
		case endpoints.AuthBaseURL + "/jwks":
			return stubJSON(req, http.StatusOK, signer.jwksJSON()), nil
		case endpoints.TokenURL():
			if err := req.ParseForm(); err != nil {
				return nil, err
			}
			nonceMu.Lock()
			currentNonce := nonce
			nonceMu.Unlock()
			return stubJSON(req, http.StatusOK, map[string]any{
				"access_token":  "access-cli",
				"refresh_token": "refresh-cli",
				"id_token":      signer.signIDToken(endpoints.Issuer(), "oaiapp_cli_issued", currentNonce),
				"token_type":    "Bearer",
				"expires_in":    3600,
				"scope":         siwc.RequestScopes,
			}), nil
		}
		return stubJSON(req, http.StatusNotFound, map[string]any{}), nil
	})
	logged := captureLog(t)

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run([]string{"login", "-credential", credentialPath})
	}()

	authorizeURL := waitForAuthorizeURL(t, logged)
	query, err := url.ParseQuery(authorizeURL[strings.Index(authorizeURL, "?")+1:])
	if err != nil {
		t.Fatal(err)
	}
	setNonce(query.Get("nonce"))
	if query.Get("client_id") != siwc.DynamicClientID || query.Get("agent_name_hint") != siwc.AgentNameHint {
		t.Fatalf("authorize query = %v", query)
	}
	if query.Get("redirect_uri") != fmt.Sprintf("http://%s%s", net.JoinHostPort(siwc.LoopbackHost, strconv.Itoa(siwc.DefaultPort)), siwc.CallbackPath) {
		t.Fatalf("redirect_uri = %q, want the official loopback callback", query.Get("redirect_uri"))
	}

	callbackURL := fmt.Sprintf("%s?code=cli-code&state=%s&client_id=oaiapp_cli_issued",
		query.Get("redirect_uri"), url.QueryEscape(query.Get("state")))
	loopbackClient := &http.Client{Transport: loopbackTransport, Timeout: 5 * time.Second}
	response, err := loopbackClient.Get(callbackURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d", response.StatusCode)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login did not finish after the callback")
	}

	credential, err := siwc.LoadCredential(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if credential.ClientID != "oaiapp_cli_issued" || credential.AccessToken != "access-cli" || credential.RefreshToken != "refresh-cli" {
		t.Fatalf("saved credential = %+v", credential)
	}
	if credential.ExtAgentHostID == "" {
		t.Fatal("saved credential has no host ID")
	}
}

func waitForAuthorizeURL(t *testing.T, logged *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(logged.String(), "\n") {
			if strings.HasPrefix(line, "http") {
				return line
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("authorize URL was not logged")
	return ""
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type cliTestSigner struct {
	key *rsa.PrivateKey
}

func newCLITestSigner(t *testing.T) *cliTestSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &cliTestSigner{key: key}
}

func (s *cliTestSigner) signIDToken(issuer, audience, nonce string) string {
	header := map[string]any{"alg": "RS256", "kid": "cli-key", "typ": "JWT"}
	claims := map[string]any{
		"iss": issuer, "aud": audience, "nonce": nonce,
		"sub": "cli-subject", "email": "cli@example.com",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	return s.sign(header, claims)
}

func (s *cliTestSigner) sign(header, claims map[string]any) string {
	headerData, _ := json.Marshal(header)
	payloadData, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(headerData) + "." + base64.RawURLEncoding.EncodeToString(payloadData)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (s *cliTestSigner) jwksJSON() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"cli-key","alg":"RS256","n":%q,"e":%q}]}`,
		base64.RawURLEncoding.EncodeToString(s.key.N.Bytes()),
		base64.RawURLEncoding.EncodeToString(big.NewInt(int64(s.key.E)).Bytes()),
	))
}
