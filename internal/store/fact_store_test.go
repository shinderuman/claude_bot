package store

import (
	"claude_bot/internal/model"
	"os"
	"testing"
)

func TestFactStore_SearchFuzzy_TargetUserName(t *testing.T) {
	// Setup temporary fact store
	tmpFile, err := os.CreateTemp("", "fact_store_test_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) // cleanup

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
