package llmtest

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude_bot/internal/prompts"
	"claude_bot/internal/siwc"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func openAITestEnv(t *testing.T, extraSettings string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)

	envPath := filepath.Join(dir, "openai.env")
	credentialPath := filepath.Join(dir, "openai.json")
	settings := fmt.Sprintf("LLM_PROVIDER=openai\nOPENAI_MODEL=test-model\nOPENAI_CREDENTIAL_FILE=%q\n", credentialPath)
	if err := os.WriteFile(envPath, []byte(settings+extraSettings), 0600); err != nil {
		t.Fatal(err)
	}
	if err := siwc.SaveCredential(credentialPath, &siwc.Credential{
		ClientID:    "oaiapp_test",
		AccessToken: "oauth-token",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scopes:      strings.Fields(siwc.RequestScopes),
		SavedAt:     time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"LLM_PROVIDER", "OPENAI_MODEL", "OPENAI_CREDENTIAL_FILE",
		"LLM_MAX_CONCURRENCY", "LLM_MAX_RETRIES", "CHARACTER_PROMPT", "CHARACTER_PRIORITY", "MAX_POST_CHARS",
		"ENABLE_IMAGE_GENERATION", "ANTHROPIC_AUTH_TOKEN", "GEMINI_MODEL", "BOT_PROFILE_FILE", "ENABLE_FACT_STORE", "REDIS_URL", "RUN_MAINTENANCE",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	return envPath
}

func TestRunOpenAIUsesPersonalInputWithoutBotData(t *testing.T) {
	envPath := openAITestEnv(t, "")
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/.env", []byte("invalid common environment"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/facts.json", []byte("invalid shared facts"), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	requests := 0
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != siwc.OfficialAPIBaseURL+siwc.ResponsesPath || req.Header.Get("Authorization") != "Bearer oauth-token" {
			t.Error("request did not use the official endpoint and the OAuth credential")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if text := string(body); !strings.Contains(text, "personal message") || !strings.Contains(text, prompts.Messages.System.ReplyFormatting) || strings.Contains(text, "返答は0文字以内") {
			t.Errorf("unexpected request body: %s", text)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"reply\"}\n\n" +
					"data: {\"type\":\"response.completed\"}\n\n")),
			Request: req,
		}, nil
	})

	if err := Run([]string{"-message", "personal message", envPath}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one request without fact collection", requests)
	}
	if !strings.Contains(logs.String(), "応答: reply") {
		t.Fatalf("CLI did not show the reply: %s", &logs)
	}
	for _, unwanted := range []string{"事実を検索中", "関連する事実は見つかりませんでした", "personal message"} {
		if strings.Contains(logs.String(), unwanted) {
			t.Errorf("unexpected CLI log: %q", unwanted)
		}
	}
}

func TestRunOpenAIGenerateImageRespectsSetting(t *testing.T) {
	const svg = `<svg xmlns="http://www.w3.org/2000/svg"/>`
	for _, setting := range []string{"false", "true"} {
		t.Run(setting, func(t *testing.T) {
			envPath := openAITestEnv(t, "ENABLE_IMAGE_GENERATION="+setting+"\n")
			requests := 0
			transport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = transport })
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				if !strings.Contains(string(body), prompts.Messages.System.ImageGeneration) {
					t.Error("request is missing the SVG generation instructions")
				}
				result := fmt.Sprintf(`{"svg":%q}`, svg)
				events := fmt.Sprintf(`data: {"type":"response.output_text.delta","delta":%q}`+"\n\n", result) +
					`data: {"type":"response.completed"}` + "\n\n"
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(events)),
					Request:    req,
				}, nil
			})

			err := Run([]string{"-mode", "generate-image", "-message", "赤い丸", envPath})
			if setting == "false" {
				if err == nil || !strings.Contains(err.Error(), "画像生成機能が無効です") || requests != 0 {
					t.Fatalf("Run() error = %v, requests = %d, want no model call", err, requests)
				}
				return
			}
			if err != nil || requests != 1 {
				t.Fatalf("Run() error = %v, requests = %d, want successful SVG generation", err, requests)
			}
			paths, err := filepath.Glob("generated_image_*.svg")
			if err != nil || len(paths) != 1 {
				t.Fatalf("SVG output = %v, %v", paths, err)
			}
			got, err := os.ReadFile(paths[0])
			if err != nil || string(got) != svg {
				t.Fatalf("generated SVG = %q, %v", got, err)
			}
		})
	}
}
