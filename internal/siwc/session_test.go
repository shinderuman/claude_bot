package siwc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixedNow() time.Time {
	now, err := time.Parse(time.RFC3339, "2026-01-02T12:00:00Z")
	if err != nil {
		panic(err)
	}
	return now
}

func expiredCredentialPath(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "credentials.json")
	credential := &Credential{
		Email:        "user@example.com",
		Subject:      "subject-1",
		ClientID:     "oaiapp_test",
		AccessToken:  "expired-access",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Scopes:       strings.Fields(RequestScopes),
		SavedAt:      fixedNow().Add(-2 * time.Hour),
	}
	if err := SaveCredential(path, credential); err != nil {
		t.Fatal(err)
	}
	return path
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubHTTPClient(handler roundTripFunc) *http.Client {
	return &http.Client{Transport: handler}
}

func jsonHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func requestBody(req *http.Request) url.Values {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		panic(err)
	}
	values, err := url.ParseQuery(string(data))
	if err != nil {
		panic(err)
	}
	return values
}

func writeTokenFormResponse(w http.ResponseWriter, body string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func tokenResponseJSON(scope string) string {
	response := map[string]any{
		"access_token":  "access-2",
		"refresh_token": "refresh-2",
		"id_token":      "id-token-2",
		"token_type":    "Bearer",
		"expires_in":    3600,
		"scope":         scope,
	}
	data, _ := json.Marshal(response)
	return string(data)
}

func testSession(t *testing.T, path string, handler roundTripFunc) *Session {
	t.Helper()
	session := NewSession(path, Endpoints{AuthBaseURL: "https://auth.test", APIBaseURL: "https://api.test"}, stubHTTPClient(handler))
	session.now = fixedNow
	return session
}

func TestSessionRefreshesExpiredTokenAndRotates(t *testing.T) {
	path := expiredCredentialPath(t)
	var requestForms []url.Values
	session := testSession(t, path, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != TokenPath || req.Method != http.MethodPost {
			return jsonHTTPResponse(req, http.StatusNotFound, "{}"), nil
		}
		form := requestBody(req)
		requestForms = append(requestForms, form)

		if form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", form.Get("grant_type"))
		}
		if form.Get("client_id") != "oaiapp_test" {
			t.Errorf("client_id = %q, want oaiapp_test", form.Get("client_id"))
		}
		if form.Get("refresh_token") != "refresh-1" {
			t.Errorf("refresh_token = %q, want refresh-1", form.Get("refresh_token"))
		}
		if form.Get("resource") != "https://api.test" {
			t.Errorf("resource = %q, want https://api.test", form.Get("resource"))
		}
		if _, hasScope := form["scope"]; hasScope {
			t.Error("refresh request must omit scope")
		}
		return jsonHTTPResponse(req, http.StatusOK, tokenResponseJSON(RequestScopes)), nil
	})

	token, err := session.EnsureAccessToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token != "access-2" {
		t.Fatalf("access token = %q, want access-2", token)
	}

	saved, err := LoadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "access-2" || saved.RefreshToken != "refresh-2" || saved.IDToken != "id-token-2" {
		t.Fatalf("rotated credential = %+v", saved)
	}
	if saved.SavedAt != fixedNow() {
		t.Fatalf("saved_at = %v, want %v", saved.SavedAt, fixedNow())
	}

	token, err = session.EnsureAccessToken(t.Context())
	if err != nil || token != "access-2" {
		t.Fatalf("second EnsureAccessToken = %q, %v; a fresh token must not refresh again", token, err)
	}
	if len(requestForms) != 1 {
		t.Fatalf("token requests = %d, want 1", len(requestForms))
	}
}

func TestSessionRefreshIsSerializedAcrossInstances(t *testing.T) {
	tests := []struct {
		name      string
		scope     string
		wantToken string
	}{
		{name: "plan usage granted", scope: RequestScopes, wantToken: "access-2"},
		{name: "plan usage declined", scope: "openid profile email"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := expiredCredentialPath(t)
			var refreshes atomic.Int32
			var mu sync.Mutex
			var seenTokens = map[string]bool{}
			handler := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				refreshes.Add(1)
				time.Sleep(50 * time.Millisecond)
				return jsonHTTPResponse(req, http.StatusOK, tokenResponseJSON(test.scope)), nil
			})

			endpoints := Endpoints{AuthBaseURL: "https://auth.test", APIBaseURL: "https://api.test"}
			first := NewSession(path, endpoints, stubHTTPClient(handler))
			second := NewSession(path, endpoints, stubHTTPClient(handler))
			first.now = fixedNow
			second.now = fixedNow

			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(useFirst bool) {
					defer wg.Done()
					session := second
					if useFirst {
						session = first
					}
					token, err := session.EnsureAccessToken(context.Background())
					if test.wantToken == "" {
						if !errors.Is(err, ErrReauthRequired) {
							t.Errorf("EnsureAccessToken() = %q, %v; want ErrReauthRequired", token, err)
						}
						return
					}
					if err != nil {
						t.Errorf("EnsureAccessToken() error = %v", err)
						return
					}
					mu.Lock()
					seenTokens[token] = true
					mu.Unlock()
				}(i%2 == 0)
			}
			wg.Wait()

			if refreshes.Load() != 1 {
				t.Fatalf("refresh requests = %d, want 1", refreshes.Load())
			}
			wantTokens := map[string]bool{}
			if test.wantToken != "" {
				wantTokens[test.wantToken] = true
			}
			if !maps.Equal(seenTokens, wantTokens) {
				t.Fatalf("returned tokens = %v, want %v", seenTokens, wantTokens)
			}
		})
	}
}

func TestSessionClearsTokensOnUnusableRefreshToken(t *testing.T) {
	for _, code := range []string{"invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused"} {
		t.Run(code, func(t *testing.T) {
			path := expiredCredentialPath(t)
			session := testSession(t, path, func(req *http.Request) (*http.Response, error) {
				return jsonHTTPResponse(req, http.StatusBadRequest, `{"error":"`+code+`"}`), nil
			})

			if _, err := session.EnsureAccessToken(t.Context()); !errors.Is(err, ErrReauthRequired) {
				t.Fatalf("EnsureAccessToken() error = %v, want ErrReauthRequired", err)
			}

			saved, err := LoadCredential(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.AccessToken != "" || saved.RefreshToken != "" {
				t.Fatalf("tokens were preserved after %s: %+v", code, saved)
			}
			if saved.ClientID != "oaiapp_test" || saved.Subject != "subject-1" {
				t.Fatalf("registration was discarded: %+v", saved)
			}

			if _, err := session.EnsureAccessToken(t.Context()); !errors.Is(err, ErrReauthRequired) {
				t.Fatalf("follow-up EnsureAccessToken() error = %v, want ErrReauthRequired", err)
			}
		})
	}
}

func TestSessionPreservesTokensOnNonTerminalRefreshError(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "invalid client", status: http.StatusUnauthorized, body: `{"error":"invalid_client"}`},
		{name: "server error", status: http.StatusInternalServerError, body: `{"error":"temporarily_unavailable"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := expiredCredentialPath(t)
			session := testSession(t, path, func(req *http.Request) (*http.Response, error) {
				resp := jsonHTTPResponse(req, test.status, test.body)
				resp.Header.Set("x-request-id", "req-refresh")
				return resp, nil
			})

			_, err := session.EnsureAccessToken(t.Context())
			var oauthErr *OAuthError
			if !errors.As(err, &oauthErr) || oauthErr.StatusCode != test.status || !strings.Contains(err.Error(), "req-refresh") {
				t.Fatalf("EnsureAccessToken() error = %v, want OAuth status and request ID", err)
			}

			saved, err := LoadCredential(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.RefreshToken != "refresh-1" {
				t.Fatalf("refresh token was discarded: %+v", saved)
			}
		})
	}
}

func TestSessionRejectsRefreshWithoutPlanUsageScope(t *testing.T) {
	path := expiredCredentialPath(t)
	session := testSession(t, path, func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(req, http.StatusOK, tokenResponseJSON("openid profile email")), nil
	})

	if _, err := session.EnsureAccessToken(t.Context()); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("EnsureAccessToken() error = %v, want ErrReauthRequired", err)
	}

	saved, err := LoadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "access-2" || saved.RefreshToken != "refresh-2" {
		t.Fatalf("refreshed tokens were not saved: %+v", saved)
	}
}

func TestSessionReportsInvalidRefreshResponse(t *testing.T) {
	for _, test := range []struct {
		field string
		body  string
	}{
		{field: "access_token", body: `{"refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600}`},
		{field: "refresh_token", body: `{"access_token":"access-2","token_type":"Bearer","expires_in":3600}`},
		{field: "token_type", body: `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"invalid","expires_in":3600}`},
		{field: "expires_in", body: `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":0}`},
	} {
		t.Run(test.field, func(t *testing.T) {
			path := expiredCredentialPath(t)
			session := testSession(t, path, func(req *http.Request) (*http.Response, error) {
				resp := jsonHTTPResponse(req, http.StatusOK, test.body)
				resp.Header.Set("x-request-id", "req-refresh")
				return resp, nil
			})

			_, err := session.EnsureAccessToken(t.Context())
			if err == nil || !strings.Contains(err.Error(), "HTTP 200 (request_id: req-refresh)") || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("EnsureAccessToken() error = %v, want response status, request ID and %s", err, test.field)
			}
			if strings.Contains(err.Error(), "access-2") || strings.Contains(err.Error(), "refresh-2") {
				t.Fatal("EnsureAccessToken() error contains tokens")
			}
			saved, err := LoadCredential(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.AccessToken != "expired-access" || saved.RefreshToken != "refresh-1" {
				t.Fatal("invalid response changed saved tokens")
			}
		})
	}
}

func TestSessionRefreshScope(t *testing.T) {
	for _, tc := range []struct {
		name          string
		responseScope string
		wantAccess    bool
	}{
		{name: "omitted scope retains the grant", wantAccess: true},
		{name: "explicit empty scope disables plan usage", responseScope: `,"scope":""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := expiredCredentialPath(t)
			session := testSession(t, path, func(req *http.Request) (*http.Response, error) {
				body := `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600` + tc.responseScope + "}"
				return jsonHTTPResponse(req, http.StatusOK, body), nil
			})
			token, err := session.EnsureAccessToken(t.Context())
			if tc.wantAccess {
				if err != nil || token != "access-2" {
					t.Fatalf("EnsureAccessToken() = %q, %v, want refreshed access", token, err)
				}
			} else if token != "" || !errors.Is(err, ErrReauthRequired) {
				t.Fatalf("EnsureAccessToken() = %q, %v, want reauthorization", token, err)
			}
		})
	}
}

func TestSessionRejectsFreshTokenWithoutPlanUsageScope(t *testing.T) {
	path := expiredCredentialPath(t)
	session := testSession(t, path, func(req *http.Request) (*http.Response, error) {
		t.Error("token endpoint must not be called for a fresh token")
		return nil, nil
	})

	credential, err := LoadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	credential.Scopes = []string{"openid", "email"}
	credential.SavedAt = fixedNow()
	if err := SaveCredential(path, credential); err != nil {
		t.Fatal(err)
	}

	if _, err := session.EnsureAccessToken(t.Context()); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("EnsureAccessToken() error = %v, want ErrReauthRequired", err)
	}
}

func TestSessionRequiresCredentialFile(t *testing.T) {
	session := testSession(t, filepath.Join(t.TempDir(), "absent.json"), func(req *http.Request) (*http.Response, error) {
		t.Error("token endpoint must not be called without a credential file")
		return nil, nil
	})

	if _, err := session.EnsureAccessToken(t.Context()); err == nil {
		t.Fatal("EnsureAccessToken() succeeded without a credential file")
	}
}
