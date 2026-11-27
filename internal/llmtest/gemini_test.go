package llmtest

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joho/godotenv"
)

func TestRunGeminiModesWithoutAnthropicToken(t *testing.T) {
	envPath, err := filepath.Abs("../../data/.env.example")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := godotenv.Read(envPath)
	if err != nil {
		t.Fatal(err)
	}
	settings["LLM_PROVIDER"] = "gemini"
	settings["GEMINI_API_KEY"] = "gemini-test-key"
	settings["GEMINI_MODEL"] = "gemini-test"
	settings["ANTHROPIC_AUTH_TOKEN"] = ""
	settings["ENABLE_FACT_STORE"] = "false"
	settings["RUN_MAINTENANCE"] = "false"
	for key, value := range settings {
		t.Setenv(key, value)
	}
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"data/facts.json", "data/facts_test.json"} {
		if err := os.WriteFile(path, []byte("壊れたJSON"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("image.png", []byte("\x89PNG\r\n\x1a\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		if req.Host != "generativelanguage.googleapis.com" || !strings.HasSuffix(req.URL.Path, "/gemini-test:generateContent") {
			t.Errorf("unexpected Gemini endpoint: %s", req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		response := `{"candidates":[{"content":{"parts":[{"text":"Geminiの応答本文"}]}}]}`
		if bytes.Contains(body, []byte("submit_json_result")) {
			response = `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"submit_json_result","args":{"result":[{"target":"user","key":"answer","value":"Geminiの応答本文"}]}}}]}}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	previousTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	for _, mode := range []string{"summary", "fact", "raw-image", "error", "response", "conversation"} {
		logs.Reset()
		before := requests.Load()
		args := []string{"-mode", mode, "-message", "test message"}
		if mode == "raw-image" {
			args = append(args, "-image", "image.png")
		}
		if err := Run(append(args, envPath)); err != nil || requests.Load() != before+1 {
			t.Fatalf("%s returned %v with %d requests\n%s", mode, err, requests.Load()-before, &logs)
		}
		if !strings.Contains(logs.String(), "Geminiの応答本文") {
			t.Fatalf("%s did not show the reply: %s", mode, &logs)
		}
		switch mode {
		case "response", "raw-image", "conversation":
			if !strings.Contains(logs.String(), "応答: Geminiの応答本文") {
				t.Fatalf("%s has a different reply format: %s", mode, &logs)
			}
		}
	}

	t.Setenv("ENABLE_FACT_STORE", "true")
	before := requests.Load()
	if err := Run([]string{"-mode", "summary", envPath}); err != nil || requests.Load() != before+1 {
		t.Fatalf("summary with facts enabled returned %v with %d requests", err, requests.Load()-before)
	}
	for _, tc := range []struct {
		flags []string
		file  string
	}{
		{file: "data/facts.json"},
		{flags: []string{"-test-facts"}, file: "data/facts_test.json"},
	} {
		before := requests.Load()
		if err := Run(append(tc.flags, envPath)); err == nil || !strings.Contains(err.Error(), "parse JSON in "+tc.file) || requests.Load() != before {
			t.Fatalf("response with invalid %s returned %v with %d requests", tc.file, err, requests.Load()-before)
		}
	}
}
