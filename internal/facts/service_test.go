package facts

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"

	gomastodon "github.com/mattn/go-mastodon"
)

func getTestService() *FactService {
	return &FactService{}
}

func TestFormatProfileText(t *testing.T) {
	service := getTestService()

	// 免責文の長さを考慮したMaxBodyLenを計算
	tests := []struct {
		name     string
		input    string
		wantEnd  string
		wantLen  int
		checkCut bool
	}{
		{
			name:    "Short text",
			input:   "Short profile.",
			wantEnd: DisclaimerText,
			wantLen: 500,
		},
		{
			name:    "Long text with separators",
			input:   strings.Repeat("あ", 300) + "\n" + strings.Repeat("い", 300),
			wantEnd: DisclaimerText,
			wantLen: 500,
		},
		{
			name:     "Long text WITHOUT separators",
			input:    strings.Repeat("無", 600),
			wantEnd:  DisclaimerText,
			wantLen:  500,
			checkCut: true,
		},
		{
			name:     "English text with periods (currently ignored)",
			input:    strings.Repeat("This is a sentence. ", 40), // approx 800 chars
			wantEnd:  DisclaimerText,
			wantLen:  500,
			checkCut: true,
		},
		{
			name:    "Exact limit length (No cut)",
			input:   strings.Repeat("あ", 456), // 500 - 44(Disclaimer length approx)
			wantEnd: DisclaimerText,
			wantLen: 500,
		},
		{
			name:     "Limit + 1 length (Forced cut)",
			input:    strings.Repeat("あ", 457), // 1 char over limit
			wantEnd:  DisclaimerText,
			wantLen:  500,
			checkCut: true,
		},
		{
			name:    "Separator exactly at limit",
			input:   strings.Repeat("あ", 455) + "。\n" + "い", // Separator at 456th char
			wantEnd: DisclaimerText,
			wantLen: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.formatProfileText(tt.input)

			// 1. 全体の長さチェック
			if len([]rune(got)) > MaxMastodonProfileChars {
				t.Errorf("Length %d > %d", len([]rune(got)), MaxMastodonProfileChars)
			}

			// 2. 免責文が含まれているか
			if !strings.HasSuffix(got, tt.wantEnd) {
				t.Errorf("Disclaimer missing or corrupted. Got suffix: %q", string([]rune(got)[len([]rune(got))-20:]))
			}

			// 3. 強制カットの確認
			if tt.checkCut {
				body := strings.TrimSuffix(got, DisclaimerText)
				expectedBodyLen := MaxMastodonProfileChars - len([]rune(DisclaimerText))
				if len([]rune(body)) != expectedBodyLen {
					t.Errorf("Body length %d != expected %d", len([]rune(body)), expectedBodyLen)
				}
			}
		})
	}
}

func TestShardingDistribution(t *testing.T) {
	totalInstances := 4
	totalFacts := 1000

	var facts []model.Fact
	for i := range totalFacts {
		facts = append(facts, model.Fact{
			Target:    "test_target",
			Key:       "key",
			Value:     fmt.Sprintf("value_%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	counts := make(map[int]int)
	processedFacts := make(map[string]bool)
	service := getTestService()

	for i := 0; i < totalInstances; i++ {
		assigned := service.shardFacts(facts, i, totalInstances)
		counts[i] = len(assigned)
		for _, f := range assigned {
			key := f.ComputeUniqueKey()
			if processedFacts[key] {
				t.Errorf("Duplicate processing detected for fact: %v", f.Value)
			}
			processedFacts[key] = true
		}
	}

	if len(processedFacts) != totalFacts {
		t.Errorf("Total processed facts mismatch. Expected %d, got %d", totalFacts, len(processedFacts))
	}

	expectedAvg := totalFacts / totalInstances
	tolerance := float64(expectedAvg) * 0.2 // Allow 20% deviation

	t.Logf("Distribution results for %d facts across %d instances:", totalFacts, totalInstances)
	for i := range totalInstances {
		count := counts[i]
		t.Logf("Instance %d: %d facts", i, count)
		if float64(count) < float64(expectedAvg)-tolerance || float64(count) > float64(expectedAvg)+tolerance {
			t.Errorf("Instance %d has unbalanced load: %d (Expected around %d)", i, count, expectedAvg)
		}
	}
}

func TestSmallBatchSkipping(t *testing.T) {
	totalInstances := 4
	threshold := 5 // 20 / 4

	var facts []model.Fact
	for i := range 5 {
		facts = append(facts, model.Fact{
			Target:    "small_target",
			Key:       "key",
			Value:     fmt.Sprintf("val_%d", i),
			Timestamp: time.Now(),
		})
	}

	t.Log("Simulating 5 facts distribution (Threshold per bot: 5):")

	anyArchived := false
	service := getTestService()
	for i := range totalInstances {
		assigned := service.shardFacts(facts, i, totalInstances)
		shouldArchive := len(assigned) >= threshold
		t.Logf("Instance %d: %d facts -> Archive? %v", i, len(assigned), shouldArchive)

		if shouldArchive {
			anyArchived = true
		}
	}

	if anyArchived {
		t.Log("Warning: One instance got all 5 facts and decided to archive. Rare but valid.")
	} else {
		t.Log("Success: No instance reached the threshold of 5. Archiving skipped as expected.")
	}
}

func TestBuildProfileFields(t *testing.T) {
	tests := []struct {
		name             string
		allowRemote      bool
		existingFields   []gomastodon.Field
		authKey          string
		wantMentionValue string
		wantSystemID     string
	}{
		{
			name:        "Allow Remote Users: True",
			allowRemote: true,
			existingFields: []gomastodon.Field{
				{Name: "Other", Value: "Value"},
			},
			authKey:          "test-auth-key",
			wantMentionValue: mastodon.MentionStatusPublic,
			wantSystemID:     "test-auth-key",
		},
		{
			name:        "Allow Remote Users: False",
			allowRemote: false,
			existingFields: []gomastodon.Field{
				{Name: "Other", Value: "Value"},
			},
			authKey:          "test-auth-key",
			wantMentionValue: mastodon.MentionStatusStopped,
			wantSystemID:     "test-auth-key",
		},
		{
			name:        "Update Existing Fields",
			allowRemote: true,
			existingFields: []gomastodon.Field{
				{Name: mastodon.ProfileFieldSystemID, Value: "old-key"},
				{Name: mastodon.ProfileFieldMentionStatus, Value: "old-status"},
				{Name: "KeepThis", Value: "Kept"},
			},
			authKey:          "new-auth-key",
			wantMentionValue: mastodon.MentionStatusPublic,
			wantSystemID:     "new-auth-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := getTestService()
			s.config = &config.Config{
				AllowRemoteUsers: tt.allowRemote,
			}

			got := s.buildProfileFields(tt.existingFields, tt.authKey)

			// Check SystemID
			foundSystemID := false
			for _, f := range got {
				if f.Name == mastodon.ProfileFieldSystemID {
					if f.Value != tt.wantSystemID {
						t.Errorf("SystemID = %v, want %v", f.Value, tt.wantSystemID)
					}
					foundSystemID = true
				}
			}
			if !foundSystemID {
				t.Error("SystemID field not found")
			}

			// Check Mention Status
			foundMention := false
			for _, f := range got {
				if f.Name == mastodon.ProfileFieldMentionStatus {
					if f.Value != tt.wantMentionValue {
						t.Errorf("MentionStatus = %v, want %v", f.Value, tt.wantMentionValue)
					}
					foundMention = true
				}
			}
			if !foundMention {
				t.Error("MentionStatus field not found")
			}

			// Check preserved fields
			foundKept := false
			for _, f := range got {
				if f.Name == "KeepThis" {
					if f.Value == "Kept" {
						foundKept = true
					}
				}
			}
			// Only check if it was expected to be there
			for _, f := range tt.existingFields {
				if f.Name == "KeepThis" && !foundKept {
					t.Error("Existing field KeepThis was lost")
				}
			}
		})
	}
}
