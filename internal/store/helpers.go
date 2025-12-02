package store

import (
	"log"
	"os"
	"path/filepath"
)

// getFilePath returns the absolute path for a file, checking the working directory first,
// then falling back to the executable directory.
func getFilePath(filename string) string {
	// data/ ディレクトリ内のファイルを優先
	localPath := filepath.Join("data", filename)
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}

	// 実行ファイルディレクトリの data/ を fallback
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal("実行ファイルパス取得エラー: ", err)
	}
	exeDir := filepath.Dir(exePath)
	return filepath.Join(exeDir, "data", filename)
}
