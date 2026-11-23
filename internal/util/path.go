package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const DataDirName = "data"

func dataDirectories() []string {
	var dirs []string
	exe, err := os.Executable()
	if err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), DataDirName))
	}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(cwd, DataDirName))
	}
	return dirs
}

func GetFilePath(filename string) (string, error) {
	if filepath.IsAbs(filename) {
		if _, err := os.Stat(filename); err != nil {
			return "", err
		}
		return filename, nil
	}
	for _, dir := range dataDirectories() {
		path := filepath.Join(dir, filename)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("設定ファイルが見つかりません: %s: %w", filename, os.ErrNotExist)
}

func DataFilePath(filename string) (string, error) {
	if filepath.IsAbs(filename) {
		return filename, nil
	}
	path, err := GetFilePath(filename)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return path, err
	}
	dir, err := GetFilePath("")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filename), nil
}
