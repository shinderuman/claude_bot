package util

import (
	"log"
	"os"
	"path/filepath"
)

// GetFilePath returns the absolute path for a file in the data directory relative to the executable.
func GetFilePath(filename string) string {
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal("実行ファイルパス取得エラー: ", err)
	}
	exeDir := filepath.Dir(exePath)
	return filepath.Join(exeDir, "data", filename)
}
