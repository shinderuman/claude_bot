package util

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestDeployRejectsLinkedDataBeforeSync(t *testing.T) {
	script, err := os.ReadFile("../../deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	workingDir := t.TempDir()
	repo := filepath.Join(workingDir, "checkout")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy.sh"), script, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module claude_bot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(repo, DataDirName)); err != nil {
		t.Fatal(err)
	}

	tools := t.TempDir()
	for _, name := range []string{"dirname", "grep", "readlink"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"rsync", "stty", "tput", "dd"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	searchDir := t.TempDir()
	otherRepo := filepath.Join(searchDir, "checkout")
	if err := os.MkdirAll(filepath.Join(otherRepo, DataDirName), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherRepo, "go.mod"), []byte("module claude_bot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "deploy.sh"), filepath.Join(otherRepo, "deploy.sh")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		script string
		cwd    string
		cdpath string
	}{
		{name: "absolute path", script: filepath.Join(repo, "deploy.sh"), cwd: t.TempDir()},
		{name: "relative path with CDPATH", script: "checkout/deploy.sh", cwd: workingDir, cdpath: searchDir},
		{name: "script link in another checkout", script: "deploy.sh", cwd: otherRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", tc.script)
			cmd.Dir = tc.cwd
			cmd.Env = []string{"PATH=" + tools, "CDPATH=" + tc.cdpath}
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "data/ がシンボリックリンクです") {
				t.Fatalf("deploy output = %s, error = %v, want rejection in the script's checkout", output, err)
			}
		})
	}
}
