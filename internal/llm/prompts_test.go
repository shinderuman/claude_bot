package llm

import (
	"claude_bot/internal/config"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-mastodon"
)

func TestBuildDailySummaryPrompt_Timezone(t *testing.T) {
	jst, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}

	utcTime := time.Date(2023, 12, 8, 15, 0, 0, 0, time.UTC)

	statuses := []*mastodon.Status{
		{
			Content:   "Test Content",
			CreatedAt: utcTime,
		},
	}

	targetDateStr := "2023/12/09"
	userRequest := ""

	prompt := BuildDailySummaryPrompt(statuses, targetDateStr, userRequest, jst)

	expectedTimeStr := "00:00"
	if !strings.Contains(prompt, expectedTimeStr) {
		t.Errorf("prompt should contain formatted JST time %q, but got:\n%s", expectedTimeStr, prompt)
	}

	unexpectedTimeStr := "15:00"
	if strings.Contains(prompt, unexpectedTimeStr) {
		t.Errorf("prompt should NOT contain UTC time %q, but got:\n%s", unexpectedTimeStr, prompt)
	}
}

func TestBuildSystemPrompt_AnalogPriority(t *testing.T) {
	cfg := &config.Config{
		BotUsername:     "testbot",
		CharacterPrompt: "CharacterPrompt",
		MaxPostChars:    500,
	}

	tests := []struct {
		name                   string
		includeCharacterPrompt bool
		priority               float64
		wantEffect             string
		wantMissing            string
		wantOrdering           bool
	}{
		{
			name:                   "Priority 0.1 (Fact Focused)",
			includeCharacterPrompt: true,
			priority:               0.1,
			wantEffect:             "あなたのキャラクター設定: 10% / データベースの事実情報: 90%",
			wantOrdering:           true,
		},
		{
			name:                   "Priority 0.9 (Character Focused)",
			includeCharacterPrompt: true,
			priority:               0.9,
			wantEffect:             "あなたのキャラクター設定: 90% / データベースの事実情報: 10%",
			wantOrdering:           true,
		},
		{
			name:                   "Summary Mode (No Character Prompt)",
			includeCharacterPrompt: false,
			priority:               0.0,
			wantMissing:            "【応答バランス指示】",
			wantOrdering:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := BuildSystemPrompt(cfg, "SessionSummary", "Facts", "", tt.includeCharacterPrompt, tt.priority)

			if tt.wantEffect != "" {
				if !strings.Contains(prompt, tt.wantEffect) {
					t.Errorf("Prompt missing expected instruction.\nExpected: %q\nGot prompt starting with: %q", tt.wantEffect, prompt[:100])
				}
			}

			if tt.wantMissing != "" {
				if strings.Contains(prompt, tt.wantMissing) {
					t.Errorf("Prompt contains forbidden instruction.\nForbidden: %q\nGot prompt starting with: %q", tt.wantMissing, prompt[:100])
				}
			}

			if tt.wantOrdering {
				idxChar := strings.Index(prompt, "CharacterPrompt")
				idxFact := strings.Index(prompt, Messages.System.KnowledgeBase)

				if idxChar == -1 || idxFact == -1 {
					t.Errorf("Expected both Character and Fact parts to be present for ordering check. CharIdx: %d, FactIdx: %d", idxChar, idxFact)
				} else {
					if tt.priority < 0.5 {
						if idxFact < idxChar {
							t.Errorf("Ordering mismatch for priority %.1f (Fact Focused).\nExpected Facts AFTER Character (Recency Bias).\nGot: Fact at %d, Character at %d", tt.priority, idxFact, idxChar)
						}
					} else {
						if idxChar < idxFact {
							t.Errorf("Ordering mismatch for priority %.1f (Character Focused).\nExpected Character AFTER Facts (Recency Bias).\nGot: Character at %d, Fact at %d", tt.priority, idxChar, idxFact)
						}
					}
				}
			}
		})
	}
}

func TestBuildSystemPrompt_FactsTruncation(t *testing.T) {
	cfg := &config.Config{
		BotUsername:     "testbot",
		CharacterPrompt: "CharacterPrompt",
		MaxPostChars:    500,
	}

	longFacts := strings.Repeat("a", 1000)

	tests := []struct {
		name                   string
		priority               float64
		includeCharacterPrompt bool
		wantLengthOrder        string
		expectedRatio          float64
	}{
		{
			name:                   "Priority 0.1 (Facts 90%) - High Retention",
			priority:               0.1,
			includeCharacterPrompt: true,
			expectedRatio:          0.9,
		},
		{
			name:                   "Priority 0.9 (Facts 10%) - Low Retention",
			priority:               0.9,
			includeCharacterPrompt: true,
			expectedRatio:          0.1,
		},
	}

	var lenLowPriority int
	var lenHighPriority int

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := BuildSystemPrompt(cfg, "", longFacts, "", tt.includeCharacterPrompt, tt.priority)

			currentLen := len(prompt)
			switch tt.priority {
			case 0.1:
				lenLowPriority = currentLen
			case 0.9:
				lenHighPriority = currentLen
			}
		})
	}

	if lenHighPriority >= lenLowPriority {
		t.Errorf("Expected prompt with Priority 0.9 (Low Facts) to be shorter than Priority 0.1. Got P0.9=%d, P0.1=%d", lenHighPriority, lenLowPriority)
	}

}

func TestBuildSystemPrompt_ProfileTruncation(t *testing.T) {
	cfg := &config.Config{
		BotUsername:     "testbot",
		CharacterPrompt: "CharacterPrompt",
		MaxPostChars:    500,
	}

	longProfile := strings.Repeat("ProfileInfo ", 100)

	tests := []struct {
		name     string
		priority float64
	}{
		{
			name:     "Priority 0.9 (Profile Truncated)",
			priority: 0.9,
		},
		{
			name:     "Priority 0.0 (Profile Retained)",
			priority: 0.0,
		},
	}

	var truncatedLen int
	var fullLen int

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := BuildSystemPrompt(cfg, "", "", longProfile, true, tt.priority)

			isTruncated := strings.Contains(prompt, "... (truncated)")

			if tt.priority == 0.9 {
				truncatedLen = len(prompt)
				if !isTruncated {
					t.Errorf("Expected truncated profile at Priority 0.9, but marker not found")
				}
			} else {
				fullLen = len(prompt)
				if isTruncated {
					t.Errorf("Expected full profile at Priority 0.1, but truncation marker found")
				}
			}
		})
	}

	if truncatedLen >= fullLen {
		t.Errorf("Expected truncated prompt (len=%d) to be shorter than full prompt (len=%d)", truncatedLen, fullLen)
	}
}
