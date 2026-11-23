package mastodon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	gomastodon "github.com/mattn/go-mastodon"
)

func TestGetStatusesByDateRangeIncludesStartAndSkipsPinnedOldPosts(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	end := start.AddDate(0, 0, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/accounts/user/statuses" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		var page []*gomastodon.Status
		switch maxID := r.URL.Query().Get("max_id"); maxID {
		case "":
			w.Header().Set("Link", "<"+r.URL.String()+"?max_id=100>; rel=\"next\"")
			page = []*gomastodon.Status{
				{ID: "50", Pinned: true, CreatedAt: start.Add(-24 * time.Hour)},
				{ID: "101", CreatedAt: end},
				{ID: "100", CreatedAt: start.Add(time.Hour)},
			}
		case "100":
			page = []*gomastodon.Status{
				{ID: "99", CreatedAt: start},
				{ID: "98", CreatedAt: start.Add(-time.Second)},
			}
		default:
			t.Errorf("unexpected max_id %q", maxID)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(page); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL})
	statuses, err := client.GetStatusesByDateRange(t.Context(), "user", start, end)
	if err != nil {
		t.Fatal(err)
	}
	var got []gomastodon.ID
	for _, status := range statuses {
		got = append(got, status.ID)
	}
	if !slices.Equal(got, []gomastodon.ID{"99", "100"}) || calls.Load() != 2 {
		t.Fatalf("statuses = %q from %d requests, want [99 100] from 2", got, calls.Load())
	}
}

func TestGetRootStatusIDUsesLastAvailableParentOnlyForMissingStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/statuses/2":
					_, _ = w.Write([]byte(`{"id":"2","in_reply_to_id":"1"}`))
				case "/api/v1/statuses/1":
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":"status unavailable"}`))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client := NewClient(Config{Server: server.URL})
			root, err := client.GetRootStatusID(t.Context(), &gomastodon.Status{ID: "3", InReplyToID: "2"})
			if status == http.StatusNotFound {
				if root != "2" || err != nil {
					t.Fatalf("root with missing ancestor = %q, %v; want last available ancestor", root, err)
				}
			} else if root != "" || err == nil {
				t.Fatalf("root with failing lookup = %q, %v; want error", root, err)
			}
		})
	}
}
