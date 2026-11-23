package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"claude_bot/internal/util"
)

func TestURLBlacklistReloadsAfterFileReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "url_blacklist.txt")
	if err := os.WriteFile(path, []byte("# ignored\n\n old.example \n"), 0600); err != nil {
		t.Fatal(err)
	}
	list, err := NewURLBlacklist(path)
	if err != nil {
		t.Fatal(err)
	}
	got := list.Get()
	if !slices.Equal(got, []string{"old.example"}) {
		t.Fatalf("initial blacklist = %q", got)
	}
	got[0] = "changed by caller"
	if list.Get()[0] != "old.example" {
		t.Fatal("Get exposed the live blacklist")
	}
	if err := list.StartWatching(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, domain := range []string{"first.example", "second.example"} {
		if err := util.WriteFileAtomically(path, []byte(domain+"\n")); err != nil {
			t.Fatal(err)
		}

		deadline := time.Now().Add(3 * time.Second)
		for !slices.Equal(list.Get(), []string{domain}) {
			if time.Now().After(deadline) {
				t.Fatalf("blacklist = %q after replacing the file with %q", list.Get(), domain)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestInitializeURLBlacklistUsesEnvironmentOnlyWhenFileIsAbsent(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}
	list, err := InitializeURLBlacklist(t.Context(), "configured.example")
	if err != nil || !slices.Equal(list.Get(), []string{"configured.example"}) {
		t.Fatalf("blacklist with no file = %v, %v", list, err)
	}

	if err := os.Mkdir(filepath.Join("data", "url_blacklist.txt"), 0700); err != nil {
		t.Fatal(err)
	}
	if list, err := InitializeURLBlacklist(t.Context(), "configured.example"); list != nil || err == nil {
		t.Fatalf("blacklist from an unreadable file = %v, %v", list, err)
	}
}
