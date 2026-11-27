package siwc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

type testJWTSigner struct {
	key *rsa.PrivateKey
	kid string
}

func newTestJWTSigner(t *testing.T) *testJWTSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &testJWTSigner{key: key, kid: "test-key-1"}
}

func (s *testJWTSigner) sign(claims map[string]any, kid string) string {
	header := map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"}
	headerData, err := json.Marshal(header)
	if err != nil {
		panic(err)
	}
	payloadData, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerData) + "." + base64.RawURLEncoding.EncodeToString(payloadData)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (s *testJWTSigner) jwksJSON() string {
	modulus := base64.RawURLEncoding.EncodeToString(s.key.N.Bytes())
	exponent := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(s.key.E)).Bytes())
	return fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":%q,"alg":"RS256","use":"sig","n":%q,"e":%q}]}`, s.kid, modulus, exponent)
}

func jwksTransport(signer *testJWTSigner) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case DiscoveryPath:
			return jsonHTTPResponse(req, 200, `{"issuer":"https://auth.test","jwks_uri":"https://auth.test/jwks"}`), nil
		case "/jwks":
			return jsonHTTPResponse(req, 200, signer.jwksJSON()), nil
		}
		return jsonHTTPResponse(req, 404, "{}"), nil
	}
}

func signedTestIDToken(signer *testJWTSigner, audience, nonce string, exp time.Time) string {
	return signer.sign(map[string]any{
		"iss":   "https://auth.test",
		"aud":   audience,
		"exp":   exp.Unix(),
		"nonce": nonce,
		"sub":   "subject-1",
		"email": "user@example.com",
	}, signer.kid)
}

var testEndpoints = Endpoints{AuthBaseURL: "https://auth.test", APIBaseURL: "https://api.test"}

func TestVerifyIDTokenAcceptsValidToken(t *testing.T) {
	signer := newTestJWTSigner(t)
	token := signedTestIDToken(signer, "oaiapp_test", "nonce-1", fixedNow().Add(time.Hour))

	claims, err := verifyIDToken(t.Context(), testEndpoints, stubHTTPClient(jwksTransport(signer)), token, "oaiapp_test", "nonce-1", fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	if claims.Sub != "subject-1" || claims.Email != "user@example.com" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestVerifyIDTokenRejectsInvalidTokens(t *testing.T) {
	expiry := fixedNow().Add(time.Hour)
	tests := []struct {
		name  string
		token func(signer *testJWTSigner) string
	}{
		{
			name: "wrong nonce",
			token: func(signer *testJWTSigner) string {
				return signedTestIDToken(signer, "oaiapp_test", "other-nonce", expiry)
			},
		},
		{
			name: "wrong audience",
			token: func(signer *testJWTSigner) string {
				return signedTestIDToken(signer, "other-client", "nonce-1", expiry)
			},
		},
		{
			name: "expired",
			token: func(signer *testJWTSigner) string {
				return signedTestIDToken(signer, "oaiapp_test", "nonce-1", fixedNow().Add(-time.Minute))
			},
		},
		{
			name: "unknown key id",
			token: func(signer *testJWTSigner) string {
				return signer.sign(map[string]any{"iss": "https://auth.test", "aud": "oaiapp_test", "exp": expiry.Unix(), "nonce": "nonce-1", "sub": "subject-1"}, "unknown-kid")
			},
		},
		{
			name: "missing subject",
			token: func(signer *testJWTSigner) string {
				return signer.sign(map[string]any{"iss": "https://auth.test", "aud": "oaiapp_test", "exp": expiry.Unix(), "nonce": "nonce-1"}, signer.kid)
			},
		},
		{
			name: "wrong issuer",
			token: func(signer *testJWTSigner) string {
				return signer.sign(map[string]any{"iss": "https://evil.example", "aud": "oaiapp_test", "exp": expiry.Unix(), "nonce": "nonce-1", "sub": "subject-1"}, signer.kid)
			},
		},
		{
			name: "tampered signature",
			token: func(signer *testJWTSigner) string {
				valid := signedTestIDToken(signer, "oaiapp_test", "nonce-1", expiry)
				return valid[:len(valid)-4] + "AAAA"
			},
		},
		{
			name:  "malformed token",
			token: func(signer *testJWTSigner) string { return "not-a-jwt" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signer := newTestJWTSigner(t)
			token := test.token(signer)
			if _, err := verifyIDToken(t.Context(), testEndpoints, stubHTTPClient(jwksTransport(signer)), token, "oaiapp_test", "nonce-1", fixedNow()); err == nil {
				t.Fatal("verifyIDToken() accepted an invalid token")
			}
		})
	}
}

func TestVerifyIDTokenRejectsDiscoveryIssuerMismatch(t *testing.T) {
	signer := newTestJWTSigner(t)
	token := signedTestIDToken(signer, "oaiapp_test", "nonce-1", fixedNow().Add(time.Hour))
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == DiscoveryPath {
			return jsonHTTPResponse(req, 200, `{"issuer":"https://evil.example","jwks_uri":"https://auth.test/jwks"}`), nil
		}
		return jsonHTTPResponse(req, 404, "{}"), nil
	})
	if _, err := verifyIDToken(t.Context(), testEndpoints, stubHTTPClient(transport), token, "oaiapp_test", "nonce-1", fixedNow()); err == nil {
		t.Fatal("verifyIDToken() accepted a discovery document with a wrong issuer")
	}
}

func TestVerifyIDTokenKeepsDiscoveryRequestID(t *testing.T) {
	signer := newTestJWTSigner(t)
	token := signedTestIDToken(signer, "oaiapp_test", "nonce-1", fixedNow().Add(time.Hour))
	client := stubHTTPClient(func(req *http.Request) (*http.Response, error) {
		resp := jsonHTTPResponse(req, http.StatusServiceUnavailable, `{}`)
		resp.Header.Set("x-request-id", "req-discovery")
		return resp, nil
	})
	_, err := verifyIDToken(t.Context(), testEndpoints, client, token, "oaiapp_test", "nonce-1", fixedNow())
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "req-discovery") {
		t.Fatalf("verifyIDToken() error = %v, want HTTP status and request ID", err)
	}
}
