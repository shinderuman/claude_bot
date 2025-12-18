package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"claude_bot/internal/model"
)

func (s *FactStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 厳密なファイルロックは行わず読み込む

	data, err := os.ReadFile(s.saveFilePath)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, &s.Facts); err != nil {
		return err
	}

	return nil
}

// Save はファイルロックを取得し、メモリ上のデータをマージして保存します (重複排除)
func (s *FactStore) Save() error {
	// タイムアウト付きでロック取得（0.5秒）
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	locked, err := s.fileLock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !locked {
		// ロック取得失敗時は保存をスキップ（次回保存時にマージされるため安全）
		log.Printf("ファイルロック取得失敗のため保存をスキップ: %v", err)
		return fmt.Errorf("failed to acquire file lock")
	}
	defer s.fileLock.Unlock() //nolint:errcheck

	s.mu.RLock()
	currentMemoryFacts := make([]model.Fact, len(s.Facts))
	copy(currentMemoryFacts, s.Facts)
	s.mu.RUnlock() // ディスク読み込み等のために一旦解除

	// 1. ディスクから最新をロード
	var diskFacts []model.Fact
	data, err := os.ReadFile(s.saveFilePath)
	if err == nil {
		// ファイルが存在する場合のみパース
		if err := json.Unmarshal(data, &diskFacts); err != nil {
			log.Printf("ファクトデータのパースエラー: %v", err)
			return fmt.Errorf("failed to parse facts: %w", err)
		}
	}

	// 2. マージ（重複排除: Target+Key+Valueが完全一致するもの）
	mergedFacts := s.mergeFacts(diskFacts, currentMemoryFacts)

	// 3. 保存
	data, err = json.MarshalIndent(mergedFacts, "", "  ")
	if err != nil {
		return err
	}

	if err := s.atomicWriteFile(data); err != nil {
		return err
	}

	// 自身の書き込みによる更新日時を反映して、直後のSyncFromDiskで無駄な読み込みを防ぐ
	if stat, err := os.Stat(s.saveFilePath); err == nil {
		s.lastModTime = stat.ModTime()
	}

	// 4. メモリも更新（他プロセスの変更を取り込む）
	s.mu.Lock()
	s.Facts = mergedFacts
	s.mu.Unlock()

	return nil
}

// mergeFacts はディスク上のデータとメモリ上のデータをマージします
// Target+Key+Valueが完全一致するもののみ重複排除します
func (s *FactStore) mergeFacts(disk, memory []model.Fact) []model.Fact {
	archiveTimestamps := make(map[string]time.Time)
	for _, f := range disk {
		if f.SourceType == model.SourceTypeArchive {
			if ts, ok := archiveTimestamps[f.Target]; !ok || f.Timestamp.After(ts) {
				archiveTimestamps[f.Target] = f.Timestamp
			}
		}
	}

	seen := make(map[string]bool)
	var result []model.Fact

	add := func(list []model.Fact, checkZombie bool) {
		for _, f := range list {
			if checkZombie {
				if archiveTime, ok := archiveTimestamps[f.Target]; ok {
					if f.SourceType != model.SourceTypeArchive && f.Timestamp.Before(archiveTime) {
						continue
					}
				}
			}

			uniqueKey := f.ComputeUniqueKey()

			if !seen[uniqueKey] {
				seen[uniqueKey] = true
				result = append(result, f)
			}
		}
	}

	add(disk, false)
	add(memory, true)

	return result
}

// SaveOverwrite forces the current memory state to disk without merging (for cleanup/maintenance)
func (s *FactStore) SaveOverwrite() error {
	// 1. Acquire lock with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	locked, err := s.fileLock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !locked {
		return fmt.Errorf("failed to acquire file lock for overwrite: %v", err)
	}
	defer s.fileLock.Unlock() //nolint:errcheck

	// 2. Serialize current memory state
	s.mu.RLock()
	data, err := json.MarshalIndent(s.Facts, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("failed to marshal facts: %w", err)
	}

	// 3. Write to disk (atomic replace)
	if err := s.atomicWriteFile(data); err != nil {
		return fmt.Errorf("failed to write facts to disk: %w", err)
	}

	// Update last modification time to avoid reloading what we just wrote
	if stat, err := os.Stat(s.saveFilePath); err == nil {
		s.lastModTime = stat.ModTime()
	}

	return nil
}

// SyncFromDisk checks for updates on disk and merges them into memory
func (s *FactStore) SyncFromDisk() error {
	stat, err := os.Stat(s.saveFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// 変更がなければスキップ
	if !stat.ModTime().After(s.lastModTime) {
		return nil
	}

	// 読み込みロック取得（他プロセスが書き込み中でないことを確認）
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	locked, err := s.fileLock.TryRLockContext(ctx, 50*time.Millisecond)
	if err != nil || !locked {
		log.Printf("SyncFromDisk: ロック取得失敗のためスキップ: %v", err)
		return nil
	}
	defer s.fileLock.Unlock() //nolint:errcheck

	// ディスクから読み込み
	data, err := os.ReadFile(s.saveFilePath)
	if err != nil {
		return err
	}

	var diskFacts []model.Fact
	if err := json.Unmarshal(data, &diskFacts); err != nil {
		return fmt.Errorf("failed to unmarshal facts: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// マージ実行
	// メモリ上のデータとディスク上のデータをマージ（ディスク優先）
	mergedFacts := s.mergeFacts(diskFacts, s.Facts)
	s.Facts = mergedFacts
	s.lastModTime = stat.ModTime()

	log.Printf("SyncFromDisk: ディスクから同期完了 (%d件)", len(s.Facts))
	return nil
}

// atomicWriteFile writes data to a temporary file and then renames it to the target path
// This ensures that the file is never in a partially written state
func (s *FactStore) atomicWriteFile(data []byte) error {
	// ターゲットファイルと同じディレクトリに一時ファイルを作成
	dir := "."
	if idx := strings.LastIndex(s.saveFilePath, string(os.PathSeparator)); idx != -1 {
		dir = s.saveFilePath[:idx]
	}

	tmpFile, err := os.CreateTemp(dir, "facts_tmp_*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// エラー発生時のクリーンアップ（成功時はRenameされるので削除不要だが、念のため）
	defer func() {
		_ = tmpFile.Close()
		// Rename成功後ならエラーになるだけなので無視、失敗時ならゴミ掃除
		_ = os.Remove(tmpPath) //nolint:errcheck
	}()

	// データを書き込み
	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	// 確実にディスクに同期
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	// 閉じる（Rename前に閉じる必要があるWindows等を考慮し、ここでも明示的に閉じる）
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// 権限設定 (0644)
	if err := os.Chmod(tmpPath, 0644); err != nil {
		log.Printf("failed to chmod temp file: %v", err)
	}

	// アトミックにリネーム
	if err := os.Rename(tmpPath, s.saveFilePath); err != nil {
		return fmt.Errorf("failed to rename temp file to target: %w", err)
	}

	return nil
}
