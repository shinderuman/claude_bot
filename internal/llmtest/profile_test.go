package llmtest

import (
	"testing"

	"claude_bot/internal/config"
)

func TestRunProfileUpdateUsesConfiguredPostLimit(t *testing.T) {
	cfg := &config.Config{MaxPostChars: 480, Timezone: "UTC", AutoPostVisibility: "unlisted"}
	if err := runProfileUpdate(cfg); err != nil {
		t.Fatal(err)
	}
}
