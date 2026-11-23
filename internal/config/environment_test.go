package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvironmentPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}

	instance := filepath.Join(dir, "instance.env")
	if err := os.WriteFile(instance, []byte("CLAUDE_BOT_TEST_OVERRIDE=instance\nCLAUDE_BOT_TEST_INSTANCE=instance\n"), 0600); err != nil {
		t.Fatal(err)
	}
	common := filepath.Join("data", ".env")
	if err := os.WriteFile(common, []byte("CLAUDE_BOT_TEST_OVERRIDE=common\nCLAUDE_BOT_TEST_INSTANCE=common\nCLAUDE_BOT_TEST_DEFAULT=common\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_BOT_TEST_OVERRIDE", "process")
	for _, name := range []string{"CLAUDE_BOT_TEST_INSTANCE", "CLAUDE_BOT_TEST_DEFAULT"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := LoadEnvironment(instance); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"CLAUDE_BOT_TEST_OVERRIDE": "process",
		"CLAUDE_BOT_TEST_INSTANCE": "instance",
		"CLAUDE_BOT_TEST_DEFAULT":  "common",
	} {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestLoadConfigReportsFirstInvalidValue(t *testing.T) {
	t.Setenv("LLM_PROVIDER", LLMProviderClaude)
	t.Setenv("LLM_TEMPERATURE", "warm")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "LLM_TEMPERATURE") {
		t.Fatalf("configuration error = %v", err)
	}
}

func TestLoadEnvironmentRequiresExplicitPath(t *testing.T) {
	if err := LoadEnvironment(""); err == nil {
		t.Fatal("loaded an environment file without an explicit path")
	}
}

func TestEnvironmentBooleanRequiresTrueOrFalse(t *testing.T) {
	const name = "CLAUDE_BOT_TEST_BOOLEAN"
	t.Setenv(name, "1")
	var env environmentReader
	if env.boolean(name) || env.err == nil {
		t.Fatal("accepted numeric boolean")
	}
}
