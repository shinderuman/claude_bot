package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"claude_bot/internal/model"

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

func TestRedisFactStoreDeleteAllFacts(t *testing.T) {
	store, server := setupRedisStore(t)
	defer store.Close()
	defer server.Close()

	store.prefix = "test*prefix"
	const factKeyCount = 205
	for i := range factKeyCount {
		server.HSet(fmt.Sprintf("%s:user-%03d", store.prefix, i), "fact", "value")
	}
	server.Set(store.prefix+TimelineKey, "index")
	server.Set(store.prefix+TargetsKey, "index")
	server.Set("test_other_prefix:keep", "other namespace")

	deleted, err := store.deleteAllFacts(t.Context())
	if err != nil || deleted != factKeyCount+2 {
		t.Fatalf("DeleteAllFacts() = %d, %v, want %d, nil", deleted, err, factKeyCount+2)
	}
	if keys := server.Keys(); len(keys) != 1 || keys[0] != "test_other_prefix:keep" {
		t.Fatalf("remaining Redis keys = %q, want only the other namespace", keys)
	}
}

func TestRedisFactStore_Workflow(t *testing.T) {
	store, mr := setupRedisStore(t)
	defer store.Close()
	defer mr.Close()

	ctx := context.Background()
	now := time.Now()

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

	facts, err := store.GetByTarget(ctx, "user1")
	if err != nil {
		t.Fatalf("GetByTarget failed: %v", err)
	}
	if len(facts) != 2 {
		t.Errorf("GetByTarget returned %d facts, want 2", len(facts))
	}

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
	for _, f := range facts {
		if f.Key == "k1" && f.Value == "v1" {
			if f.Author != "updated_bot" {
				t.Errorf("Fact not updated, author=%s", f.Author)
			}
		}
	}

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

	recent, err := store.GetRecent(ctx, 2)
	if err != nil {
		t.Fatalf("GetRecent failed: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("GetRecent returned %d, want 2", len(recent))
	}

	sort.Slice(recent, func(i, j int) bool {
		return recent[i].Timestamp.After(recent[j].Timestamp)
	})

	if recent[0].Value != "v1_new" {
		t.Errorf("Most recent should be v1_new, got %v", recent[0].Value)
	}
	if recent[1].Value != "v1" {
		t.Errorf("Second recent should be v1, got %v", recent[1].Value)
	}

	results, err := store.SearchFuzzy(ctx, []string{"User1"}, []string{"k1"})
	if err != nil {
		t.Fatalf("SearchFuzzy failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("SearchFuzzy found %d facts, want 2", len(results))
	}

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

func TestRedisFactStoreReplaceAllFacts(t *testing.T) {
	storage, server := setupRedisStore(t)
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = storage.Close() })
	ctx := t.Context()
	old := model.Fact{Target: "old", Key: "topic", Value: "previous"}
	newFact := model.Fact{Target: "new:target", Key: "topic", Value: "replacement"}
	if err := storage.Add(ctx, old); err != nil {
		t.Fatal(err)
	}
	server.Set("different:keep", "unrelated")

	if err := storage.ReplaceAllFacts(ctx, []model.Fact{newFact, {Target: "timeline", Key: "topic", Value: "reserved"}}); err == nil {
		t.Fatal("import with a reserved target succeeded")
	}
	all, err := storage.GetAllFacts(ctx)
	if err != nil || len(all) != 1 || all[0].Value != old.Value {
		t.Fatalf("existing facts after failed import = %+v, %v", all, err)
	}
	for _, key := range server.Keys() {
		if strings.HasPrefix(key, "claude-migrate-") {
			t.Fatalf("failed import leaked staging key %q", key)
		}
	}

	if err := storage.ReplaceAllFacts(ctx, []model.Fact{newFact, newFact}); err != nil {
		t.Fatal(err)
	}
	recent, err := storage.GetRecent(ctx, 10)
	if err != nil || len(recent) != 1 || recent[0].Target != newFact.Target || recent[0].Value != newFact.Value {
		t.Fatalf("timeline after import = %+v, %v", recent, err)
	}
	if !server.Exists("different:keep") || server.Exists(storage.targetKey(old.Target)) {
		t.Fatalf("unexpected keys after import: %q", server.Keys())
	}
	for _, key := range server.Keys() {
		if strings.HasPrefix(key, "claude-migrate-") || server.TTL(key) > 0 {
			t.Fatalf("key %q retained staging state", key)
		}
	}
}

func TestRedisFactStoreRetentionKeepsIndexesInSync(t *testing.T) {
	storage, server := setupRedisStore(t)
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = storage.Close() })
	ctx := t.Context()

	facts := []model.Fact{
		{Target: "user:remote", Key: "topic", Value: "older", Timestamp: time.Now().Add(-time.Hour)},
		{Target: "user:remote", Key: "topic", Value: "newer", Timestamp: time.Now()},
	}
	for _, fact := range facts {
		if err := storage.Add(ctx, fact); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := storage.EnforceMaxFacts(ctx, 1); removed != 1 || err != nil {
		t.Fatalf("EnforceMaxFacts = %d, %v", removed, err)
	}
	byTarget, err := storage.GetByTarget(ctx, facts[0].Target)
	if err != nil || len(byTarget) != 1 || byTarget[0].Value != "newer" {
		t.Fatalf("facts after retention = %+v, %v", byTarget, err)
	}
	if removed, err := storage.Remove(ctx, facts[0].Target, func(model.Fact) bool { return true }); removed != 1 || err != nil {
		t.Fatalf("Remove = %d, %v", removed, err)
	}
	recent, err := storage.GetRecent(ctx, 10)
	if err != nil || len(recent) != 0 {
		t.Fatalf("timeline after deletion = %+v, %v", recent, err)
	}
}

func TestMaintenanceStopsWhenFactsCannotBeListed(t *testing.T) {
	storage, server := setupRedisStore(t)
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = storage.Close() })
	for _, value := range []string{"first", "second"} {
		if err := storage.Add(t.Context(), model.Fact{Target: "user", Key: "topic", Value: value}); err != nil {
			t.Fatal(err)
		}
	}
	server.Del(storage.prefix + TargetsKey)
	if err := server.Set(storage.prefix+TargetsKey, "invalid targets index"); err != nil {
		t.Fatal(err)
	}

	factStore := NewFactStore(storage, "")
	if expired, overLimit, err := factStore.PerformMaintenance(t.Context(), 30, 1); expired != 0 || overLimit != 0 || err == nil {
		t.Fatalf("maintenance = %d expired, %d over limit, %v", expired, overLimit, err)
	}
	if facts, err := storage.GetByTarget(t.Context(), "user"); err != nil || len(facts) != 2 {
		t.Fatalf("facts after failed maintenance = %+v, %v", facts, err)
	}
}

func TestRedisRejectsNonStringFactValues(t *testing.T) {
	storage, server := setupRedisStore(t)
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = storage.Close() })

	server.HSet(storage.targetKey("user"), "hash", `{"target":"user","key":"score","value":42}`)
	if _, err := storage.GetByTarget(t.Context(), "user"); err == nil {
		t.Fatal("read returned successfully with a non-string fact value")
	}
}
