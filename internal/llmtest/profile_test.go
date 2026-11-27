package llmtest

import (
	"os"
	"path/filepath"
	"testing"

	"claude_bot/internal/config"
)

func TestRunProfileUpdateUsesConfiguredPostLimit(t *testing.T) {
	cfg := &config.Config{MaxPostChars: 480, Timezone: "UTC", AutoPostVisibility: "unlisted"}
	if err := runProfileUpdate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestRunProfileWithoutLLMCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_CREDENTIAL_FILE", filepath.Join(dir, "missing.json"))
	for _, key := range []string{"MAX_POST_CHARS", "TIMEZONE", "AUTO_POST_VISIBILITY", "ALLOW_REMOTE_USERS"} {
		t.Setenv(key, "")
	}
	envPath := filepath.Join(dir, "profile.env")
	if err := os.WriteFile(envPath, []byte("LLM_PROVIDER=openai\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"-mode", "profile", envPath}); err != nil {
		t.Fatal(err)
	}
}
