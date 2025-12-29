package util

import (
	"log"
	"os"
	"path/filepath"
)

const DataDirName = "data"

func GetFilePath(filename string) string {
	exe, err := os.Executable()
	if err == nil {
		path := filepath.Join(filepath.Dir(exe), DataDirName, filename)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		path := filepath.Join(cwd, DataDirName, filename)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	log.Fatal("設定ファイルが見つかりません: ", filename)
	return ""
}
