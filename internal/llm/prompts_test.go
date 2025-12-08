package llm

import (
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-mastodon"
)

func TestBuildDailySummaryPrompt_Timezone(t *testing.T) {
	// Setup timezone (JST)
	jst, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}

	// Create a sample status with UTC time
	// 2023-12-08 15:00:00 UTC -> 2023-12-09 00:00:00 JST
	utcTime := time.Date(2023, 12, 8, 15, 0, 0, 0, time.UTC)

	statuses := []*mastodon.Status{
		{
			Content:   "Test Content",
			CreatedAt: utcTime,
		},
	}

	targetDateStr := "2023/12/09"
	userRequest := ""

	// Generate prompt
	prompt := BuildDailySummaryPrompt(statuses, targetDateStr, userRequest, jst)

	// Verify timestamp in generated prompt
	// Expected: "00:00" (JST) because 15:00 UTC is 00:00 JST next day
	expectedTimeStr := "00:00"
	if !strings.Contains(prompt, expectedTimeStr) {
		t.Errorf("prompt should contain formatted JST time %q, but got:\n%s", expectedTimeStr, prompt)
	}

	// Verify it does NOT contain UTC time
	unexpectedTimeStr := "15:00"
	if strings.Contains(prompt, unexpectedTimeStr) {
		t.Errorf("prompt should NOT contain UTC time %q, but got:\n%s", unexpectedTimeStr, prompt)
	}
}
