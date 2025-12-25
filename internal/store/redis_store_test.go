package store

import (
	"claude_bot/internal/model"
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func setupRedisStore(t *testing.T) (*RedisFactStore, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	url := fmt.Sprintf("redis://%s", mr.Addr())
	store, err := NewRedisFactStore(url, "test_prefix")
	if err != nil {
		mr.Close()
		t.Fatalf("failed to create redis store: %v", err)
	}

	return store, mr
}

func TestRedisFactStore_Workflow(t *testing.T) {
	store, mr := setupRedisStore(t)
	defer store.Close()
	defer mr.Close()

	ctx := context.Background()
	now := time.Now()

	// 1. Add Facts
	fact1 := model.Fact{
		Target:         "user1",
		TargetUserName: "User1",
		Key:            "k1",
		Value:          "v1",
		Author:         "bot",
		Timestamp:      now,
	}
	if err := store.Add(ctx, fact1); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	fact2 := model.Fact{
		Target:    "user1",
		Key:       "k2",
		Value:     "v2",
		Timestamp: now.Add(1 * time.Second),
	}
	if err := store.Add(ctx, fact2); err != nil {
		t.Fatalf("Add fact2 failed: %v", err)
	}

	// 2. GetByTarget
	facts, err := store.GetByTarget(ctx, "user1")
	if err != nil {
		t.Fatalf("GetByTarget failed: %v", err)
	}
	if len(facts) != 2 {
		t.Errorf("GetByTarget returned %d facts, want 2", len(facts))
	}

	// 3. Update Existing (Same Key+Value -> Same Hash)
	fact1Updated := fact1
	fact1Updated.Author = "updated_bot"
	fact1Updated.Timestamp = now.Add(2 * time.Second)
	if err := store.Add(ctx, fact1Updated); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	facts, _ = store.GetByTarget(ctx, "user1")
	if len(facts) != 2 {
		t.Errorf("After update, expected 2 facts (deduplicated), got %d", len(facts))
	}
	// Verify update content
	for _, f := range facts {
		if f.Key == "k1" && f.Value == "v1" {
			if f.Author != "updated_bot" {
				t.Errorf("Fact not updated, author=%s", f.Author)
			}
		}
	}

	// 4. Add Same Key, Diff Value (Should Append)
	fact3 := model.Fact{
		Target:         "user1",
		TargetUserName: "User1",
		Key:            "k1",
		Value:          "v1_new",
		Timestamp:      now.Add(3 * time.Second),
	}
	if err := store.Add(ctx, fact3); err != nil {
		t.Fatalf("Add fact3 failed: %v", err)
	}

	facts, _ = store.GetByTarget(ctx, "user1")
	if len(facts) != 3 {
		t.Errorf("With diff value, expected 3 facts, got %d", len(facts))
	}

	// 5. GetRecent
	recent, err := store.GetRecent(ctx, 2)
	if err != nil {
		t.Fatalf("GetRecent failed: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("GetRecent returned %d, want 2", len(recent))
	}
	// Expected order: fact3 (newest), fact1Updated (2nd), fact2 (3rd).
	// Timestamps:
	// fact1Updated: now+2s
	// fact3: now+3s
	// fact2: now+1s
	// So recent should be fact3, fact1Updated

	// Sort by Time Desc for checking
	sort.Slice(recent, func(i, j int) bool {
		return recent[i].Timestamp.After(recent[j].Timestamp)
	})

	if recent[0].Value != "v1_new" {
		t.Errorf("Most recent should be v1_new, got %v", recent[0].Value)
	}
	if recent[1].Value != "v1" {
		t.Errorf("Second recent should be v1, got %v", recent[1].Value)
	}

	// 6. SearchFuzzy
	results, err := store.SearchFuzzy(ctx, []string{"User1"}, []string{"k1"})
	if err != nil {
		t.Fatalf("SearchFuzzy failed: %v", err)
	}
	if len(results) != 2 {
		// Expect v1 (k1) and v1_new (k1)
		t.Errorf("SearchFuzzy found %d facts, want 2", len(results))
	}

	// 7. Remove
	deleted, err := store.Remove(ctx, "user1", func(f model.Fact) bool {
		return f.Key == "k1"
	})
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if deleted != 2 {
		t.Errorf("Remove deleted %d facts, want 2", deleted)
	}

	facts, _ = store.GetByTarget(ctx, "user1")
	if len(facts) != 1 {
		t.Errorf("Remaining facts %d, want 1", len(facts))
	}
	if facts[0].Key != "k2" {
		t.Errorf("Wrong remaining fact: %v", facts[0])
	}
}

func TestRedisFactStore_GetAllFacts(t *testing.T) {
	store, mr := setupRedisStore(t)
	defer store.Close()
	defer mr.Close()
	ctx := context.Background()

	store.Add(ctx, model.Fact{Target: "t1", Key: "k", Value: "v"})
	store.Add(ctx, model.Fact{Target: "t2", Key: "k", Value: "v"})

	all, err := store.GetAllFacts(ctx)
	if err != nil {
		t.Fatalf("GetAllFacts failed: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("GetAllFacts returned %d, want 2", len(all))
	}
}
