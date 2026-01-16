package llm

import (
	"claude_bot/internal/config"
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
		wantEffect             string // Expected specific substring
		wantMissing            string // Substring that MUST NOT be present
		wantOrdering           bool   // Whether to check ordering (Recency Bias)
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
			priority:               0.0, // Should be ignored
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

			// Verify Recency Bias Ordering
			if tt.wantOrdering {
				idxChar := strings.Index(prompt, "CharacterPrompt")
				// factsPart 固有のヘッダー（weightInstruction内の「データベースの事実情報」と区別するため完全一致）
				idxFact := strings.Index(prompt, "以下はデータベースに保存されている確認済みの事実情報です")

				// If Facts are present in the prompt (passed as "Facts" argument, so should be there if not empty)
				// In this test setup, we pass "Facts" as relevantFacts so header should exist.

				if idxChar == -1 || idxFact == -1 {
					t.Errorf("Expected both Character and Fact parts to be present for ordering check. CharIdx: %d, FactIdx: %d", idxChar, idxFact)
				} else {
					if tt.priority < 0.5 {
						// Low Priority = Fact Focused = Facts should be LAST (Recency Bias)
						// Expected: Character ... Facts
						if idxFact < idxChar {
							t.Errorf("Ordering mismatch for priority %.1f (Fact Focused).\nExpected Facts AFTER Character (Recency Bias).\nGot: Fact at %d, Character at %d", tt.priority, idxFact, idxChar)
						}
					} else {
						// High Priority = Character Focused = Character should be LAST
						// Expected: Facts ... Character
						if idxChar < idxFact {
							t.Errorf("Ordering mismatch for priority %.1f (Character Focused).\nExpected Character AFTER Facts (Recency Bias).\nGot: Character at %d, Fact at %d", tt.priority, idxChar, idxFact)
						}
					}
				}
			}
		})
	}
}
