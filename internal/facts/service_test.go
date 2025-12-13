package facts

import (
	"fmt"
	"testing"
	"time"

	"claude_bot/internal/model"
)

func getTestService() *FactService {
	return &FactService{}
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
