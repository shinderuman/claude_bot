package util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDataPaths(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	dataDir := filepath.Join(root, DataDirName)
	if err := os.Mkdir(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dataDir, "fixture.txt")
	if err := os.WriteFile(existing, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := GetFilePath("fixture.txt"); got != existing || err != nil {
		t.Fatalf("GetFilePath() = %q, %v", got, err)
	}
	if _, err := GetFilePath("missing.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("GetFilePath(missing) = %v", err)
	}
	if got, err := DataFilePath("new.txt"); got != filepath.Join(dataDir, "new.txt") || err != nil {
		t.Fatalf("DataFilePath() = %q, %v", got, err)
	}
}
