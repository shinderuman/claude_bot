package util

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGetFilePath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get CWD: %v", err)
	}

	dataDir := filepath.Join(cwd, "..", "..", "data")

	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		dataDir = filepath.Join(cwd, "test_data_dir")
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			t.Fatalf("Failed to create temp data dir: %v", err)
		}
		defer os.RemoveAll(dataDir)
	}

	testFilename := "path_test_dummy.txt"
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Failed to resolve project root: %v", err)
	}

	realDataDir := filepath.Join(projectRoot, "data")
	testFilePath := filepath.Join(realDataDir, testFilename)

	if err := os.WriteFile(testFilePath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(testFilePath)

	originalWd, _ := os.Getwd()
	defer os.Chdir(originalWd) //nolint:errcheck

	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("Failed to change dir to project root: %v", err)
	}

	foundPath := GetFilePath(testFilename)

	if foundPath != testFilePath {
		t.Errorf("Expected path %s, got %s", testFilePath, foundPath)
	}
}

func TestGetFilePath_Fatal(t *testing.T) {
	if os.Getenv("BE_CRASHER") == "1" {
		GetFilePath("non_existent_file_definitely_12345")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestGetFilePath_Fatal")
	cmd.Env = append(os.Environ(), "BE_CRASHER=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatalf("process ran with err %v, want exit status 1", err)
}
