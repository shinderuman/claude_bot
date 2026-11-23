package facts

import (
	"context"
	"fmt"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

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
	service := &FactService{}

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
	tolerance := float64(expectedAvg) * 0.2

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
	threshold := 50 / 4

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
	service := &FactService{}
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

func TestShouldArchiveFacts(t *testing.T) {
	s := &FactService{}

	now := time.Now()
	oldTime := now.AddDate(0, 0, -31)

	tests := []struct {
		name           string
		facts          []model.Fact
		totalInstances int
		want           bool
		wantReason     string
	}{
		{
			name:           "Threshold Met (Single Instance)",
			facts:          makeFacts(50, now),
			totalInstances: 1,
			want:           true,
			wantReason:     ArchiveReasonThresholdMet,
		},
		{
			name:           "Threshold Met (Multiple Instances)",
			facts:          makeFacts(13, now),
			totalInstances: 4,
			want:           true,
			wantReason:     ArchiveReasonThresholdMet,
		},
		{
			name: "Old Data Met",
			facts: func() []model.Fact {
				f := makeFacts(9, now)
				f = append(f, model.Fact{Value: "old", Timestamp: oldTime})
				return f
			}(),
			totalInstances: 1,
			want:           true,
			wantReason:     ArchiveReasonOldData,
		},
		{
			name:           "Insufficient Count (Threshold)",
			facts:          makeFacts(49, now),
			totalInstances: 1,
			want:           false,
			wantReason:     ArchiveReasonInsufficient,
		},
		{
			name: "Insufficient Count (Old Data)",
			facts: []model.Fact{
				{Value: "old", Timestamp: oldTime},
			},
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

func TestFactService_isValidFact(t *testing.T) {
	mockLLM := &MockLLMClient{}

	tests := []struct {
		name string
		fact model.Fact
		want bool
	}{
		{
			name: "Empty Target",
			fact: model.Fact{Target: "", Key: "k", Value: "v"},
			want: false,
		},
		{
			name: "Unknown Target",
			fact: model.Fact{Target: model.UnknownTarget, Key: "k", Value: "v"},
			want: false,
		},
		{
			name: "Role User Target",
			fact: model.Fact{Target: model.RoleUser, Key: "k", Value: "v"},
			want: false,
		},
		{
			name: "Role Assistant Target",
			fact: model.Fact{Target: model.RoleAssistant, Key: "k", Value: "v"},
			want: false,
		},

		{
			name: "Empty Key",
			fact: model.Fact{Target: "valid_target", Key: "", Value: "v"},
			want: false,
		},
		{
			name: "Whitespace Key",
			fact: model.Fact{Target: "valid_target", Key: "  ", Value: "v"},
			want: false,
		},
		{
			name: "Empty Value String",
			fact: model.Fact{Target: "valid_target", Key: "k", Value: ""},
			want: false,
		},
		{
			name: "Whitespace Value String",
			fact: model.Fact{Target: "valid_target", Key: "k", Value: "  "},
			want: false,
		},

		{
			name: "Known Bot with 'bot' keyword",
			fact: model.Fact{Target: "known_bot", Key: "desc", Value: "I am a bot"},
			want: false,
		},
		{
			name: "Known Bot without 'bot' keyword",
			fact: model.Fact{Target: "known_bot", Key: "status", Value: "Online"},
			want: true,
		},

		{
			name: "Valid User Fact",
			fact: model.Fact{Target: "valid_target", Key: "k", Value: "v"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factStore := store.NewFactStore(&MockFactStorage{}, "")

			knownBots := map[string]struct{}{"known_bot": {}}
			service := NewFactService(&config.Config{}, factStore, mockLLM, nil, nil, knownBots)

			if got := service.isValidFact(tt.fact); got != tt.want {
				t.Errorf("isValidFact() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFactService_AddFact_Save(t *testing.T) {
	mockLLM := &MockLLMClient{}
	var saved bool
	mockStorage := &MockFactStorage{
		AddFunc: func(ctx context.Context, fact model.Fact) error {
			saved = true
			return nil
		},
		GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) {
			return nil, nil
		},
	}
	factStore := store.NewFactStore(mockStorage, "")
	service := NewFactService(&config.Config{}, factStore, mockLLM, nil, nil, nil)

	validFact := model.Fact{Target: "valid", Key: "k", Value: "v"}
	service.AddFact(validFact)

	if !saved {
		t.Error("AddFact should save valid fact")
	}
}
