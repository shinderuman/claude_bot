package store

import (
	"claude_bot/internal/model"
	"claude_bot/internal/slack"
	"context"
	"os"
	"testing"
	"time"
)

func TestFactStore_SearchFuzzy_TargetUserName(t *testing.T) {
	// Setup temporary fact store
	tmpFile, err := os.CreateTemp("", "fact_store_test_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(tmpFile.Name())
	})

	store := NewFactStore(tmpFile.Name(), slack.NewClient("", "", ""))

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
	store.Facts = facts

	results1 := store.SearchFuzzy([]string{"user1@example.com"}, []string{"preference"})
	if len(results1) != 1 {
		t.Errorf("Search by Target failed: got %d results, want 1", len(results1))
	}

	results2 := store.SearchFuzzy([]string{"User1"}, []string{"preference"})
	if len(results2) != 1 {
		t.Errorf("Search by TargetUserName failed: got %d results, want 1", len(results2))
	}
	if len(results2) > 0 && results2[0].Target != "user1@example.com" {
		t.Errorf("Unexpected result target: %s", results2[0].Target)
	}

	results3 := store.SearchFuzzy([]string{"User1"}, []string{"pref"})
	if len(results3) != 1 {
		t.Errorf("Search by Partial Key failed: got %d results, want 1", len(results3))
	}

	store.Facts = append(store.Facts, model.Fact{
		Target:         "longname@example.com",
		TargetUserName: "LongNameUser",
		Key:            "status",
		Value:          "Active",
	})

	results4 := store.SearchFuzzy([]string{"LongN"}, []string{"status"})
	if len(results4) != 1 {
		t.Errorf("Prefix Match (5 chars) failed: got %d results, want 1", len(results4))
	}

	results5 := store.SearchFuzzy([]string{"eUser"}, []string{"status"})
	if len(results5) != 1 {
		t.Errorf("Suffix Match (5 chars) failed: got %d results, want 1", len(results5))
	}

	results6 := store.SearchFuzzy([]string{"Long"}, []string{"status"})
	if len(results6) != 0 {
		t.Errorf("Short Prefix Search should fail: got %d results, want 0", len(results6))
	}

	results7 := store.SearchFuzzy([]string{"nomatch"}, []string{"preference"})
	if len(results7) != 0 {
		t.Errorf("Mismatch Search should fail: got %d results, want 0", len(results7))
	}
}

func TestFactStore_RemoveFacts(t *testing.T) {
	// Setup temporary fact store
	tmpFile, err := os.CreateTemp("", "fact_store_test_remove_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(tmpFile.Name())
	})

	store := NewFactStore(tmpFile.Name(), slack.NewClient("", "", ""))

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
	store.Facts = facts

	deleted, err := store.RemoveFacts(context.Background(), target, func(f model.Fact) bool {
		return f.Value == "delete"
	})

	if err != nil {
		t.Errorf("RemoveFacts returned error: %v", err)
	}

	if deleted != 1 {
		t.Errorf("RemoveFacts deleted count = %d, want 1", deleted)
	}

	if len(store.Facts) != 2 {
		t.Errorf("Store facts count = %d, want 2", len(store.Facts))
	}

	store2 := NewFactStore(tmpFile.Name(), slack.NewClient("", "", ""))
	if len(store2.Facts) != 2 {
		t.Errorf("Disk facts count = %d, want 2 (persistence verification)", len(store2.Facts))
	}

	for _, f := range store2.Facts {
		if f.Value == "delete" {
			t.Error("Deleted fact found on disk")
		}
	}
}

func TestFactStore_IO_SaveAndLoad(t *testing.T) {
	// Setup temporary fact store
	tmpFile, err := os.CreateTemp("", "fact_store_test_io_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(tmpFile.Name())
	})
	// Remove file so NewFactStore treats it as new
	_ = os.Remove(tmpFile.Name())

	store := NewFactStore(tmpFile.Name(), slack.NewClient("", "", ""))

	fact := model.Fact{
		Target: "io_test",
		Key:    "io_key",
		Value:  "io_value",
	}
	store.AddFactWithSource(fact)

	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Create new store instance pointing to same file
	store2 := NewFactStore(tmpFile.Name(), slack.NewClient("", "", ""))

	if len(store2.Facts) != 1 {
		t.Errorf("Loaded facts count = %d, want 1", len(store2.Facts))
	} else {
		if store2.Facts[0].Value != "io_value" {
			t.Errorf("Loaded value mismatch: %v", store2.Facts[0].Value)
		}
	}
}

func TestFactStore_Cleanup(t *testing.T) {
	// Setup temporary fact store
	tmpFile, err := os.CreateTemp("", "fact_store_test_cleanup_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(tmpFile.Name())
	})

	store := NewFactStore(tmpFile.Name(), slack.NewClient("", "", ""))

	now := time.Now()
	oldTime := now.AddDate(0, 0, -31) // 31 days old

	store.Facts = []model.Fact{
		{Value: "new", Timestamp: now},
		{Value: "old", Timestamp: oldTime},
	}

	removed := store.Cleanup(30 * 24 * time.Hour)

	if removed != 1 {
		t.Errorf("Cleanup removed %d, want 1", removed)
	}

	if len(store.Facts) != 1 {
		t.Errorf("Remaining facts %d, want 1", len(store.Facts))
	}

	if store.Facts[0].Value != "new" {
		t.Error("Wrong fact remaining")
	}
}

func TestFactStore_GetRecentFacts(t *testing.T) {
	store := &FactStore{}
	now := time.Now()

	store.Facts = []model.Fact{
		{Value: "oldest", Timestamp: now.Add(-3 * time.Hour)},
		{Value: "newest", Timestamp: now},
		{Value: "middle", Timestamp: now.Add(-1 * time.Hour)},
	}

	recent := store.GetRecentFacts(2)

	if len(recent) != 2 {
		t.Fatalf("GetRecentFacts returned %d, want 2", len(recent))
	}

	if recent[0].Value != "middle" {
		t.Errorf("First result should be middle (last inserted), got %v", recent[0].Value)
	}
	if recent[1].Value != "newest" {
		t.Errorf("Second result should be newest (second last), got %v", recent[1].Value)
	}
}

func TestFactStore_ZombieProtection(t *testing.T) {
	// Setup
	tmpFile, err := os.CreateTemp("", "fact_store_zombie_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) //nolint:errcheck

	store := NewFactStore(tmpPath, slack.NewClient("", "", ""))

	factA := model.Fact{Target: "t1", Key: "A", Value: "valA", Timestamp: time.Now().Add(-1 * time.Hour)}
	store.Facts = []model.Fact{factA}
	store.Save() //nolint:errcheck

	emptyData := []byte("[]")
	if err := os.WriteFile(tmpPath, emptyData, 0644); err != nil {
		t.Fatalf("failed to simulate external deletion: %v", err)
	}

	store.Facts = append(store.Facts, model.Fact{Target: "t1", Key: "B", Value: "valB", Timestamp: time.Now()})

	time.Sleep(100 * time.Millisecond)

	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	finalStore := NewFactStore(tmpPath, slack.NewClient("", "", ""))

	hasA := false
	hasB := false
	for _, f := range finalStore.Facts {
		if f.Key == "A" {
			hasA = true
		}
		if f.Key == "B" {
			hasB = true
		}
	}

	if hasA {
		t.Error("Zombie Fact A resurrected! It should have been dropped.")
	}
	if !hasB {
		t.Error("New Fact B missing! It should have been saved.")
	}
}
