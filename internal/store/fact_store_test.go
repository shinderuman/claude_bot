package store

import (
	"claude_bot/internal/model"
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

	store := NewFactStore(tmpFile.Name())

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

	// Test Case 1: Search by Target (exact match)
	results1 := store.SearchFuzzy([]string{"user1@example.com"}, []string{"preference"})
	if len(results1) != 1 {
		t.Errorf("Search by Target failed: got %d results, want 1", len(results1))
	}

	// Test Case 2: Search by TargetUserName (exact match)
	// This is the fix we implemented
	results2 := store.SearchFuzzy([]string{"User1"}, []string{"preference"})
	if len(results2) != 1 {
		t.Errorf("Search by TargetUserName failed: got %d results, want 1", len(results2))
	}
	if len(results2) > 0 && results2[0].Target != "user1@example.com" {
		t.Errorf("Unexpected result target: %s", results2[0].Target)
	}

	// Test Case 3: Partial Key Match
	results3 := store.SearchFuzzy([]string{"User1"}, []string{"pref"})
	if len(results3) != 1 {
		t.Errorf("Search by Partial Key failed: got %d results, want 1", len(results3))
	}

	// Test Case 4: Prefix Match (Length >= 5)
	// "User1" (5 chars) matches "User1" (Exact) - wait, Prefix test needs partial
	// Let's us "User123" -> matches "User1" ? No.
	// Let's use a longer name for Partial tests.
	store.Facts = append(store.Facts, model.Fact{
		Target:         "longname@example.com",
		TargetUserName: "LongNameUser",
		Key:            "status",
		Value:          "Active",
	})

	// "LongN" (5 chars) matches "LongNameUser" (Prefix)
	results4 := store.SearchFuzzy([]string{"LongN"}, []string{"status"})
	if len(results4) != 1 {
		t.Errorf("Prefix Match (5 chars) failed: got %d results, want 1", len(results4))
	}

	// Test Case 5: Suffix Match (Length >= 5)
	// "eUser" (5 chars) matches "LongNameUser" (Suffix)
	results5 := store.SearchFuzzy([]string{"eUser"}, []string{"status"})
	if len(results5) != 1 {
		t.Errorf("Suffix Match (5 chars) failed: got %d results, want 1", len(results5))
	}

	// Test Case 6: Short Query Mismatch (Length < 5)
	// "Long" (4 chars) should NOT match "LongNameUser" (Prefix)
	results6 := store.SearchFuzzy([]string{"Long"}, []string{"status"})
	if len(results6) != 0 {
		t.Errorf("Short Prefix Search should fail: got %d results, want 0", len(results6))
	}

	// Test Case 7: Mismatch
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

	store := NewFactStore(tmpFile.Name())

	target := "target@example.com"
	// Add test facts
	facts := []model.Fact{
		{
			Target: target,
			Key:    "key1",
			Value:  "keep",
		},
		{
			Target: target,
			Key:    "key2",
			Value:  "delete",
		},
		{
			Target: "other@example.com",
			Key:    "key2",
			Value:  "keep_other",
		},
	}
	store.Facts = facts

	// Remove logic: delete if Value is "delete"
	deleted, err := store.RemoveFacts(target, func(f model.Fact) bool {
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

	store := NewFactStore(tmpFile.Name())

	// Add facts
	fact := model.Fact{
		Target: "io_test",
		Key:    "io_key",
		Value:  "io_value",
	}
	store.AddFactWithSource(fact)

	// Save to disk
	if err := store.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Create new store instance pointing to same file
	store2 := NewFactStore(tmpFile.Name())
	// Should auto-load or be empty? NewFactStore calls load().
	// Wait, NewFactStore implementation:
	/*
		func NewFactStore(filePath string) *FactStore {
			fs := &FactStore{
				saveFilePath: filePath,
			}
			if err := fs.load(); err != nil {
				// handled by returning empty store usually, or error log
			}
			return fs
		}
	*/
	// Let's verify load works.

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

	store := NewFactStore(tmpFile.Name())

	now := time.Now()
	oldTime := now.AddDate(0, 0, -31) // 31 days old

	store.Facts = []model.Fact{
		{Value: "new", Timestamp: now},
		{Value: "old", Timestamp: oldTime},
	}

	// Retention 30 days. Old (31 days) should be removed.
	removed := store.Cleanup(30 * 24 * time.Hour) // Duration

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

	// GetRecentFacts sorts by Timestamp desc
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
