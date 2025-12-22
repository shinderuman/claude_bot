package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestGetKnownBotUsernames(t *testing.T) {
	// Setup temporary directory for test
	tempDir, err := os.MkdirTemp("", "discovery_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create data directory
	dataDir := filepath.Join(tempDir, "data")
	if err := os.Mkdir(dataDir, 0755); err != nil {
		t.Fatalf("Failed to create data dir: %v", err)
	}

	// Change CWD to tempDir so util.GetFilePath finds "data"
	originalWd, _ := os.Getwd()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalWd)
	}()

	// Create test files
	files := map[string]string{
		".env.bot1":    "BOT_USERNAME=bot_one\n",
		".env.bot2":    "BOT_USERNAME=bot_two\n",
		".env.ignore":  "OTHER_VAR=value\n",          // No BOT_USERNAME
		".env.example": "BOT_USERNAME=example_bot\n", // Should be ignored
		"other.txt":    "BOT_USERNAME=hidden_bot\n",  // Should be ignored
		".env.broken":  "BOT_USERNAME=",              // Empty username
	}

	for name, content := range files {
		path := filepath.Join(dataDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file %s: %v", name, err)
		}
	}

	// Execution
	usernames, err := GetKnownBotUsernames()
	if err != nil {
		t.Fatalf("GetKnownBotUsernames returned error: %v", err)
	}

	// Verification
	expected := []string{"bot_one", "bot_two"}
	sort.Strings(expected)
	sort.Strings(usernames)

	if !reflect.DeepEqual(usernames, expected) {
		t.Errorf("Expected %v, got %v", expected, usernames)
	}
}
