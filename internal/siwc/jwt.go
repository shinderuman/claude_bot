package siwc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type idTokenClaims struct {
	Iss   string          `json:"iss"`
	Aud   json.RawMessage `json:"aud"`
	Exp   int64           `json:"exp"`
	Nonce string          `json:"nonce"`
	Sub   string          `json:"sub"`
	Email string          `json:"email"`
}

type discoveryDocument struct {
	Issuer  string `json:"issuer"`
	JwksURI string `json:"jwks_uri"`
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func verifyIDToken(ctx context.Context, endpoints Endpoints, httpClient *http.Client, token, audience, nonce string, now time.Time) (*idTokenClaims, error) {
	headerSegment, payloadSegment, signingInput, signature, err := splitJWT(token)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(ctx, endpoints, httpClient, headerSegment, signingInput, signature); err != nil {
		return nil, err
	}
	claims, err := decodeClaims(payloadSegment)
	if err != nil {
		return nil, err
	}
	if err := validateClaims(claims, endpoints.Issuer(), audience, nonce, now); err != nil {
		return nil, err
	}
	return claims, nil
}

func splitJWT(token string) (header, payload, signingInput string, signature []byte, err error) {
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return "", "", "", nil, fmt.Errorf("IDトークンの形式が不正です")
	}
	signature, err = base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		return "", "", "", nil, fmt.Errorf("IDトークン署名を解析できません: %w", err)
	}
	return segments[0], segments[1], segments[0] + "." + segments[1], signature, nil
}

func decodeClaims(payload string) (*idTokenClaims, error) {
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("IDトークン本体を解析できません: %w", err)
	}
	var claims idTokenClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, fmt.Errorf("IDトークン本体を解析できません: %w", err)
	}
	return &claims, nil
}

func validateClaims(claims *idTokenClaims, issuer, audience, nonce string, now time.Time) error {
	if claims.Iss != issuer {
		return fmt.Errorf("IDトークンの発行者が不正です: %s", claims.Iss)
	}
	if now.Unix() >= claims.Exp {
		return fmt.Errorf("IDトークンが期限切れです")
	}
	accepted, err := audienceAccepts(claims.Aud, audience)
	if err != nil || !accepted {
		return fmt.Errorf("IDトークンのaudienceが一致しません")
	}
	if claims.Nonce != nonce {
		return fmt.Errorf("IDトークンのnonceが一致しません")
	}
	if claims.Sub == "" {
		return fmt.Errorf("IDトークンにsubjectがありません")
	}
	return nil
}

func audienceAccepts(raw json.RawMessage, audience string) (bool, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == audience, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return false, err
	}
	return slices.Contains(list, audience), nil
}

func verifySignature(ctx context.Context, endpoints Endpoints, httpClient *http.Client, headerSegment, signingInput string, signature []byte) error {
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	headerData, err := base64.RawURLEncoding.DecodeString(headerSegment)
	if err != nil {
		return fmt.Errorf("IDトークンヘッダーを解析できません: %w", err)
	}
	if err := json.Unmarshal(headerData, &header); err != nil {
		return fmt.Errorf("IDトークンヘッダーを解析できません: %w", err)
	}
	if header.Alg != "RS256" {
		return fmt.Errorf("IDトークンの署名アルゴリズムが不正です: %s", header.Alg)
	}

	publicKey, err := fetchJWKSPublicKey(ctx, endpoints, httpClient, header.Kid)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return fmt.Errorf("IDトークン署名の検証に失敗しました: %w", err)
	}
	return nil
}

func fetchJWKSPublicKey(ctx context.Context, endpoints Endpoints, httpClient *http.Client, kid string) (*rsa.PublicKey, error) {
	var discovery discoveryDocument
	if err := fetchJSON(ctx, httpClient, endpoints.DiscoveryURL(), &discovery); err != nil {
		return nil, fmt.Errorf("OpenAIのOpenID構成を取得できません: %w", err)
	}
	if discovery.Issuer != endpoints.Issuer() {
		return nil, fmt.Errorf("OpenID構成の発行者が不正です: %s", discovery.Issuer)
	}

	jwksURL, err := url.Parse(discovery.JwksURI)
	if err != nil || jwksURL.User != nil || jwksURL.Scheme+"://"+jwksURL.Host != endpoints.Issuer() {
		return nil, fmt.Errorf("OpenID構成のJWKS URLが不正です")
	}
	var jwks jwksDocument
	if err := fetchJSON(ctx, httpClient, jwksURL.String(), &jwks); err != nil {
		return nil, fmt.Errorf("OpenAIのJWKSを取得できません: %w", err)
	}
	for _, key := range jwks.Keys {
		if key.Kid != kid || key.Kty != "RSA" {
			continue
		}
		return rsaPublicKey(key)
	}
	return nil, fmt.Errorf("JWKSに一致する鍵がありません (kid: %s)", kid)
}

func fetchJSON(ctx context.Context, httpClient *http.Client, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	status := responseStatus(resp.StatusCode, resp.Header.Get("x-request-id"))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", status, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: 応答読込失敗: %w", status, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%s: 応答解析失敗: %w", status, err)
	}
	return nil
}

func rsaPublicKey(key jwkKey) (*rsa.PublicKey, error) {
	modulus, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("JWKのnを解析できません: %w", err)
	}
	exponent, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("JWKのeを解析できません: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulus),
		E: int(new(big.Int).SetBytes(exponent).Int64()),
	}, nil
}
