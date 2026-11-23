package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRegisterNodeMergesAndPrunesMembership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	fixture := fmt.Sprintf(`{"nodes":[
		{"username":"self","joined_at":%q,"last_updated":%q},
		{"username":"other","joined_at":%q,"last_updated":%q},
		{"username":"other","joined_at":%q,"last_updated":%q},
		{"username":"expired","joined_at":%q,"last_updated":%q}
	]}`,
		"2020-01-01T00:00:00Z", time.Now().UTC().Format(time.RFC3339),
		"2019-01-01T00:00:00Z", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"2021-01-01T00:00:00Z", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		"2010-01-01T00:00:00Z", "2010-01-01T00:00:00Z")
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}

	index, count, err := registerNode(t.Context(), path, "self")
	if err != nil || index != 1 || count != 2 {
		t.Fatalf("registerNode = %d, %d, %v", index, count, err)
	}
	registry, err := readRegistry(path)
	if err != nil || len(registry.Nodes) != 2 {
		t.Fatalf("registry = %+v, %v", registry, err)
	}
	other := registry.Nodes[0]
	if other.Username != "other" || other.JoinedAt.Year() != 2019 || time.Since(other.LastUpdated) > 2*time.Minute {
		t.Fatalf("merged node = %+v", other)
	}
}

func TestRegisterNodePreservesConcurrentMembership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	var wg sync.WaitGroup
	for _, username := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := registerNode(t.Context(), path, username); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	registry, err := readRegistry(path)
	if err != nil || len(registry.Nodes) != 3 {
		t.Fatalf("registry = %+v, %v", registry, err)
	}
}

func TestRegisterNodePreservesMalformedRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registerNode(t.Context(), path, "self"); err == nil {
		t.Fatal("registerNode succeeded on malformed data")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{" {
		t.Fatalf("registry = %q, %v", data, err)
	}
}
