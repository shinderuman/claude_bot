package store

import (
	"claude_bot/internal/model"
	"context"
	"testing"
	"time"
)

func TestFactStore_SearchFuzzy_TargetUserName(t *testing.T) {
	store := NewMemoryFactStore()

	// Add test facts
	facts := []model.Fact{
		{
			Target:         "user1@example.com",
			TargetUserName: "User1",
			Key:            "preference",
			Value:          "Soba",
		},
		{
			Target:         "other@example.com",
			TargetUserName: "OtherUser",
			Key:            "hobby",
			Value:          "Programming",
		},
	}
	// Use Add instead of direct slice access
	for _, f := range facts {
		store.Add(context.Background(), f)
	}

	results1, _ := store.SearchFuzzy(context.Background(), []string{"user1@example.com"}, []string{"preference"})
	if len(results1) != 1 {
		t.Errorf("Search by Target failed: got %d results, want 1", len(results1))
	}

	results2, _ := store.SearchFuzzy(context.Background(), []string{"User1"}, []string{"preference"})
	if len(results2) != 1 {
		t.Errorf("Search by TargetUserName failed: got %d results, want 1", len(results2))
	}
	if len(results2) > 0 && results2[0].Target != "user1@example.com" {
		t.Errorf("Unexpected result target: %s", results2[0].Target)
	}

	results3, _ := store.SearchFuzzy(context.Background(), []string{"User1"}, []string{"pref"})
	if len(results3) != 1 {
		t.Errorf("Search by Partial Key failed: got %d results, want 1", len(results3))
	}

	store.Add(context.Background(), model.Fact{
		Target:         "longname@example.com",
		TargetUserName: "LongNameUser",
		Key:            "status",
		Value:          "Active",
	})

	results4, _ := store.SearchFuzzy(context.Background(), []string{"LongN"}, []string{"status"})
	if len(results4) != 1 {
		t.Errorf("Prefix Match (5 chars) failed: got %d results, want 1", len(results4))
	}

	results5, _ := store.SearchFuzzy(context.Background(), []string{"eUser"}, []string{"status"})
	if len(results5) != 1 {
		t.Errorf("Suffix Match (5 chars) failed: got %d results, want 1", len(results5))
	}

	results6, _ := store.SearchFuzzy(context.Background(), []string{"Long"}, []string{"status"})
	if len(results6) != 0 {
		t.Errorf("Short Prefix Search should fail: got %d results, want 0", len(results6))
	}

	results7, _ := store.SearchFuzzy(context.Background(), []string{"nomatch"}, []string{"preference"})
	if len(results7) != 0 {
		t.Errorf("Mismatch Search should fail: got %d results, want 0", len(results7))
	}
}

func TestFactStore_RemoveFacts(t *testing.T) {
	store := NewMemoryFactStore()

	target := "target@example.com"
	facts := []model.Fact{
		{
			Target:    target,
			Key:       "key1",
			Value:     "keep",
			Timestamp: time.Now(),
		},
		{
			Target:    target,
			Key:       "key2",
			Value:     "delete",
			Timestamp: time.Now(),
		},
		{
			Target:    "other@example.com",
			Key:       "key2",
			Value:     "keep_other",
			Timestamp: time.Now(),
		},
	}
	for _, f := range facts {
		store.Add(context.Background(), f)
	}

	deleted, err := store.Remove(context.Background(), target, func(f model.Fact) bool {
		return f.Value == "delete"
	})

	if err != nil {
		t.Errorf("RemoveFacts returned error: %v", err)
	}

	if deleted != 1 {
		t.Errorf("RemoveFacts deleted count = %d, want 1", deleted)
	}

	allFacts, _ := store.GetAllFacts(context.Background())
	if len(allFacts) != 2 {
		t.Errorf("Store facts count = %d, want 2", len(allFacts))
	}

	// Verify persistence in memory (MemoryStore doesn't use disk, so skipping "disk verification")
	// but we can check if it's gone from retrieval
	remaining, _ := store.GetByTarget(context.Background(), target)
	for _, f := range remaining {
		if f.Value == "delete" {
			t.Error("Deleted fact found in store")
		}
	}
}

// IO SaveAndLoad test removed as MemoryStore doesn't implement File I/O directly in this interface logic
// Logic for verifying persistence will be in RedisStore tests.
// The previous IO tests tested the specific file operations of Legacy FactStore.

// Cleanup test rewritten to use manual removal logic or if interface supports it (it doesn't explicitly have Cleanup(duration))
// But we can test Remove with time condition
func TestFactStore_Cleanup_Logic(t *testing.T) {
	store := NewMemoryFactStore()

	now := time.Now()
	oldTime := now.AddDate(0, 0, -31) // 31 days old

	store.Add(context.Background(), model.Fact{Value: "new", Timestamp: now, Target: "t", Key: "k1"})
	store.Add(context.Background(), model.Fact{Value: "old", Timestamp: oldTime, Target: "t", Key: "k2"})

	// Simulate Cleanup logic using Remove
	threshold := now.Add(-30 * 24 * time.Hour)
	removed, _ := store.Remove(context.Background(), "t", func(f model.Fact) bool {
		return f.Timestamp.Before(threshold)
	})

	if removed != 1 {
		t.Errorf("Cleanup removed %d, want 1", removed)
	}

	allFacts, _ := store.GetAllFacts(context.Background())
	if len(allFacts) != 1 {
		t.Errorf("Remaining facts %d, want 1", len(allFacts))
	}

	if allFacts[0].Value != "new" {
		t.Error("Wrong fact remaining")
	}
}

func TestFactStore_GetRecentFacts(t *testing.T) {
	store := NewMemoryFactStore()
	now := time.Now()

	store.Add(context.Background(), model.Fact{Value: "oldest", Timestamp: now.Add(-3 * time.Hour)})
	store.Add(context.Background(), model.Fact{Value: "newest", Timestamp: now})
	store.Add(context.Background(), model.Fact{Value: "middle", Timestamp: now.Add(-1 * time.Hour)})

	recent, _ := store.GetRecent(context.Background(), 2)

	if len(recent) != 2 {
		t.Fatalf("GetRecentFacts returned %d, want 2", len(recent))
	}

	if recent[0].Value != "newest" {
		t.Errorf("First result should be newest, got %v", recent[0].Value)
	}
	if recent[1].Value != "middle" {
		t.Errorf("Second result should be middle, got %v", recent[1].Value)
	}
}

// ZombieProtection test removed as it tested File I/O concurrency logic specific to the old implementation.

// RemoveFactsByKey is not in interface but can be simulated or added if essential.
// Plan listed RemoveFactsByKey in "interface definition" in doc?
// Doc: GetByTarget, SearchFuzzy, Remove, Backup. RemoveByKey is not explicitly in interface.go I wrote.
// But we can test Remove with Key filter.
func TestFactStore_RemoveFactsByKey_Simulation(t *testing.T) {
	store := NewMemoryFactStore()

	target := "target@example.com"
	key := "target_key"
	facts := []model.Fact{
		{Target: target, Key: key, Value: "v1", Timestamp: time.Now()},
		{Target: target, Key: key, Value: "v2", Timestamp: time.Now()}, // Duplicate key logic in MemoryStore might update this?
		// MemoryStore.Add updates if Target/Key matches and Value matches? No, logic:
		// matches Target/Key/Value -> update.
		// matches Target/Key but Value diff -> append.
		// So duplicate keys are allowed if values differ.
		{Target: target, Key: "other_key", Value: "v3", Timestamp: time.Now()},
		{Target: "other", Key: key, Value: "v4", Timestamp: time.Now()},
	}
	for _, f := range facts {
		store.Add(context.Background(), f)
	}

	// Remove by Key
	count, _ := store.Remove(context.Background(), target, func(f model.Fact) bool {
		return f.Key == key
	})

	if count != 2 {
		t.Errorf("Removed count = %d, want 2", count)
	}

	allFacts, _ := store.GetAllFacts(context.Background())
	if len(allFacts) != 2 {
		t.Errorf("Remaining facts = %d, want 2", len(allFacts))
	}

	for _, f := range allFacts {
		if f.Target == target && f.Key == key {
			t.Error("Found fact that should be removed")
		}
	}
}

func TestFactStore_SearchFuzzy_ColleagueProfile(t *testing.T) {
	store := NewMemoryFactStore()

	// Add test facts
	facts := []model.Fact{
		{
			// Target is self (bot)
			Target:         "bot_user",
			TargetUserName: "MyBot",
			// Key starts with system:colleague_profile
			Key: "system:colleague_profile:12345",
			// Value contains the name we want to search for
			Value: "Name: Tanaka Taro\nBio: A very nice colleague.",
		},
		{
			Target:         "bot_user",
			TargetUserName: "MyBot",
			Key:            "preference",
			Value:          "Sushi",
		},
	}
	for _, f := range facts {
		store.Add(context.Background(), f)
	}

	// 1. Search by "Tanaka" (contained in Value)
	results1, _ := store.SearchFuzzy(context.Background(), []string{"bot_user"}, []string{"Tanaka"})

	if len(results1) != 1 {
		t.Errorf("Search for name within Value failed: got %d results, want 1", len(results1))
	} else {
		if results1[0].Key != "system:colleague_profile:12345" {
			t.Errorf("Got wrong fact: %s", results1[0].Key)
		}
	}

	// 2. Search by "colleague" (Key prefix search - should still work if key matches)
	results2, _ := store.SearchFuzzy(context.Background(), []string{"bot_user"}, []string{"colleague"})
	if len(results2) != 1 {
		t.Errorf("Search by Key part failed: got %d results, want 1", len(results2))
	}

	// 3. Search by "Sushi" (Regular fact, Value search should NOT happen for non-system keys)
	results3, _ := store.SearchFuzzy(context.Background(), []string{"bot_user"}, []string{"Sushi"})
	if len(results3) != 0 {
		t.Errorf("Regular fact value search should not happen: got %d results, want 0", len(results3))
	}
}
