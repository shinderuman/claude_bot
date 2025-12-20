package facts

import (
	"fmt"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"

	gomastodon "github.com/mattn/go-mastodon"
)

func getTestService() (*FactService, *mastodon.Client) {
	m := &mastodon.Client{}
	s := &FactService{
		mastodonClient: m,
		config:         &config.Config{},
	}
	return s, m
}

// TestFormatProfileText was removed as the logic was moved to mastodon.Client and decoupled.
// See internal/mastodon/profile_test.go for TestFormatProfileBody and TestTruncateText.

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
	service, _ := getTestService()

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
	service, _ := getTestService()
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
		timezone         string
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
			timezone:         "UTC",
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
			timezone:         "UTC",
		},
		{
			name:        "Update Existing Managed Fields & Preserve Others",
			allowRemote: true,
			existingFields: []gomastodon.Field{
				{Name: "First", Value: "1"}, // Should stay first
				{Name: mastodon.ProfileFieldSystemID, Value: "old-key"},
				{Name: mastodon.ProfileFieldLastUpdated, Value: "old-time"},
				{Name: "Second", Value: "2"}, // Should stay second (before managed fields)
				{Name: mastodon.ProfileFieldMentionStatus, Value: "old-status"},
			},
			authKey:          "new-auth-key",
			wantMentionValue: mastodon.MentionStatusPublic,
			wantSystemID:     "new-auth-key",
			timezone:         "UTC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, m := getTestService()
			cfg := &config.Config{
				AllowRemoteUsers: tt.allowRemote,
				Timezone:         tt.timezone,
			}
			s.config = cfg

			got := m.BuildProfileFields(cfg, tt.existingFields, tt.authKey)

			// 1. Verify Managed Fields Exist and match values
			managedFound := map[string]bool{
				mastodon.ProfileFieldSystemID:      false,
				mastodon.ProfileFieldMentionStatus: false,
				mastodon.ProfileFieldLastUpdated:   false,
			}

			for _, f := range got {
				if f.Name == mastodon.ProfileFieldSystemID {
					if f.Value != tt.wantSystemID {
						t.Errorf("SystemID = %v, want %v", f.Value, tt.wantSystemID)
					}
					managedFound[mastodon.ProfileFieldSystemID] = true
				}
				if f.Name == mastodon.ProfileFieldMentionStatus {
					if f.Value != tt.wantMentionValue {
						t.Errorf("MentionStatus = %v, want %v", f.Value, tt.wantMentionValue)
					}
					managedFound[mastodon.ProfileFieldMentionStatus] = true
				}
				if f.Name == mastodon.ProfileFieldLastUpdated {
					// Time check is loose (just check it exists and has length)
					if len(f.Value) == 0 {
						t.Error("LastUpdated field is empty")
					}
					managedFound[mastodon.ProfileFieldLastUpdated] = true
				}
			}

			for k, found := range managedFound {
				if !found {
					t.Errorf("Managed field %s not found", k)
				}
			}

			// 2. Verify Order
			// Expected order: [User Fields...] -> SystemID -> MentionStatus -> LastUpdated

			// Find indices
			var idxSystemID, idxMention, idxLastUpdated = -1, -1, -1
			for i, f := range got {
				switch f.Name {
				case mastodon.ProfileFieldSystemID:
					idxSystemID = i
				case mastodon.ProfileFieldMentionStatus:
					idxMention = i
				case mastodon.ProfileFieldLastUpdated:
					idxLastUpdated = i
				}
			}

			if idxSystemID == -1 || idxMention == -1 || idxLastUpdated == -1 {
				t.Fatal("One or more managed fields missing, cannot verify order")
			}

			if idxSystemID > idxMention {
				t.Errorf("Order invalid: SystemID (%d) should be before MentionStatus (%d)", idxSystemID, idxMention)
			}
			if idxMention > idxLastUpdated {
				t.Errorf("Order invalid: MentionStatus (%d) should be before LastUpdated (%d)", idxMention, idxLastUpdated)
			}

			// Verify User fields are BEFORE managed fields
			for i, f := range got {
				if _, isManaged := managedFound[f.Name]; !isManaged {
					if i > idxSystemID { // SystemID is the first managed field
						t.Errorf("User field %s found at index %d, expected before SystemID (%d)", f.Name, i, idxSystemID)
					}
				}
			}
		})
	}
}

func TestBuildProfileFields_OrderGuarantee(t *testing.T) {
	_, m := getTestService()
	cfg := &config.Config{
		AllowRemoteUsers: true,
		Timezone:         "UTC",
	}

	// Input with scrambled order and managed fields mixed with user fields
	input := []gomastodon.Field{
		{Name: mastodon.ProfileFieldLastUpdated, Value: "old-time"},     // Should move to end
		{Name: "UserField1", Value: "1"},                                // Should be first
		{Name: mastodon.ProfileFieldMentionStatus, Value: "old-status"}, // Should move to middle
		{Name: "UserField2", Value: "2"},                                // Should be second
		{Name: mastodon.ProfileFieldSystemID, Value: "old-key"},         // Should move to start of managed block
	}

	authKey := "new-auth-key"
	got := m.BuildProfileFields(cfg, input, authKey)

	// Expected Order:
	// 1. UserField1
	// 2. UserField2
	// 3. SystemID
	// 4. MentionStatus
	// 5. LastUpdated

	if len(got) != 5 {
		t.Fatalf("Expected 5 fields, got %d", len(got))
	}

	expectedNames := []string{
		"UserField1",
		"UserField2",
		mastodon.ProfileFieldSystemID,
		mastodon.ProfileFieldMentionStatus,
		mastodon.ProfileFieldLastUpdated,
	}

	for i, name := range expectedNames {
		if got[i].Name != name {
			t.Errorf("Index %d: expected name %s, got %s", i, name, got[i].Name)
		}
	}

	// Verify SystemID update
	if got[2].Value != authKey {
		t.Errorf("SystemID value mismatch: got %s, want %s", got[2].Value, authKey)
	}
}

func TestIsValidFact(t *testing.T) {
	s, _ := getTestService()
	tests := []struct {
		name   string
		target string
		key    string
		value  interface{}
		want   bool
	}{
		{"Valid Fact", "test-user", "hobby", "programming", true},
		{"Invalid Target", "unknown", "hobby", "programming", false},
		{"Invalid Target (Case)", "UNKNOWN", "hobby", "programming", false},
		{"Invalid Target (None)", "none", "hobby", "programming", false},
		{"Invalid Key (ID)", "test-user", "user-id", "123", false},
		{"Invalid Key (Follower)", "test-user", "follower_count", "100", false},
		{"Invalid Value (Unknown)", "test-user", "hobby", "不明", false},
		{"Invalid Value (None)", "test-user", "hobby", "なし", false},
		{"Invalid Value (Short)", "test-user", "hobby", "a", false},
		{"Valid Value (Number)", "test-user", "height", 170, true}, // Check non-string
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.isValidFact(tt.target, tt.key, tt.value); got != tt.want {
				t.Errorf("isValidFact() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeKey(t *testing.T) {
	s, _ := getTestService()
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"No Mapping", "hobby", "hobby"},
		{"Mapping: Preference", "好きなもの", "preference"},
		{"Mapping: Preference (Partial)", "私が好きなもの", "preference"},
		{"Mapping: Location", "居住地", "location"},
		{"Mapping: Possession", "ペット", "possession"},
		{"Capitalized", "Hobby", "hobby"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.normalizeKey(tt.key); got != tt.want {
				t.Errorf("normalizeKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldArchiveFacts(t *testing.T) {
	s, _ := getTestService()

	// Constants from service.go (assumed based on logic)
	// ArchiveFactThreshold = 10
	// ArchiveMinFactCount  = 2
	// ArchiveAgeDays       = 30

	now := time.Now()
	oldTime := now.AddDate(0, 0, -31) // 31 days ago

	tests := []struct {
		name           string
		facts          []model.Fact
		totalInstances int
		want           bool
		wantReason     string
	}{
		{
			name:           "Threshold Met (Single Instance)",
			facts:          makeFacts(10, now), // 10 facts
			totalInstances: 1,
			want:           true,
			wantReason:     ArchiveReasonThresholdMet,
		},
		{
			name:           "Threshold Met (Multiple Instances)",
			facts:          makeFacts(3, now), // 10 / 4 = 2.5 -> 3 >= 2
			totalInstances: 4,
			want:           true,
			wantReason:     ArchiveReasonThresholdMet,
		},
		{
			name: "Old Data Met",
			facts: []model.Fact{
				{Value: "old", Timestamp: oldTime},
				{Value: "new", Timestamp: now},
			}, // 2 facts, one old
			totalInstances: 1,
			want:           true,
			wantReason:     ArchiveReasonOldData,
		},
		{
			name:           "Insufficient Count (Threshold)",
			facts:          makeFacts(9, now),
			totalInstances: 1,
			want:           false,
			wantReason:     ArchiveReasonInsufficient,
		},
		{
			name: "Insufficient Count (Old Data)",
			facts: []model.Fact{
				{Value: "old", Timestamp: oldTime},
			}, // Only 1 fact, need 2
			totalInstances: 1,
			want:           false,
			wantReason:     ArchiveReasonInsufficient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := s.shouldArchiveFacts(tt.facts, tt.totalInstances)
			if got != tt.want {
				t.Errorf("shouldArchiveFacts() = %v, want %v", got, tt.want)
			}
			if got && reason != tt.wantReason {
				t.Errorf("Reason = %v, want %v", reason, tt.wantReason)
			}
		})
	}
}

// Helper to create facts
func makeFacts(count int, ts time.Time) []model.Fact {
	var facts []model.Fact
	for i := 0; i < count; i++ {
		facts = append(facts, model.Fact{
			Value:     fmt.Sprintf("val%d", i),
			Timestamp: ts,
		})
	}
	return facts
}
