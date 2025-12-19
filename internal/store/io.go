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

	// Set lastModTime to establish baseline
	if stat, err := os.Stat(s.saveFilePath); err == nil {
		s.lastModTime = stat.ModTime()
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
	lastSyncTime := s.lastModTime // Capture sync time for zombie checking
	s.mu.RUnlock()                // ディスク読み込み等のために一旦解除

	// 1. ディスクから最新をロード
	var diskFacts []model.Fact
	data, err := os.ReadFile(s.saveFilePath)
	if err == nil {
		// ファイルが存在する場合のみパース
		if len(data) > 0 {
			if err := json.Unmarshal(data, &diskFacts); err != nil {
				log.Printf("ファクトデータのパースエラー: %v", err)
				return fmt.Errorf("failed to parse facts: %w", err)
			}
		}
	}

	// 2. マージ（重複排除 & ゾンビ排除）
	mergedFacts := s.mergeFacts(diskFacts, currentMemoryFacts, lastSyncTime)

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

// mergeFacts merges disk and memory facts.
// It detects potential "Zombie Facts" (facts present in memory but missing from disk)
// and discards them if they are older than lastSyncTime (implying another process deleted them).
func (s *FactStore) mergeFacts(disk, memory []model.Fact, lastSync time.Time) []model.Fact {
	// 1. Build map of disk facts for fast lookup
	diskMap := make(map[string]bool)
	archiveTimestamps := make(map[string]time.Time)

	for _, f := range disk {
		uniqueKey := f.ComputeUniqueKey()
		diskMap[uniqueKey] = true

		if f.SourceType == model.SourceTypeArchive {
			if ts, ok := archiveTimestamps[f.Target]; !ok || f.Timestamp.After(ts) {
				archiveTimestamps[f.Target] = f.Timestamp
			}
		}
	}

	seen := make(map[string]bool)
	var result []model.Fact

	// Helper to add fact if unique
	addFact := func(f model.Fact) {
		uniqueKey := f.ComputeUniqueKey()
		if !seen[uniqueKey] {
			seen[uniqueKey] = true
			result = append(result, f)
		}
	}

	// 2. Add all disk facts (Source of Truth)
	for _, f := range disk {
		addFact(f)
	}

	// 3. Add memory facts ONLY if they are valid
	for _, f := range memory {
		uniqueKey := f.ComputeUniqueKey()

		// If in disk, it was already added (or we can just skip to avoid duplicates logic, handled by seen)
		if diskMap[uniqueKey] {
			continue
		}

		// ZOMBIE CHECK:
		// If NOT in disk, check timestamp against lastSyncTime.
		// If fact is older than lastSyncTime, it means it existed when we last synced,
		// but now it's gone from disk. Someone else deleted it. -> DROP IT.
		if !f.Timestamp.IsZero() && !lastSync.IsZero() && f.Timestamp.Before(lastSync) {
			// This is a Zombie. Drop it.
			// log.Printf("Dropped Zombie Fact: %s", uniqueKey)
			continue
		}

		// Logic for Archive overwrite (from original code)
		if archiveTime, ok := archiveTimestamps[f.Target]; ok {
			if f.SourceType != model.SourceTypeArchive && f.Timestamp.Before(archiveTime) {
				continue
			}
		}

		// Valid new/preserved fact
		addFact(f)
	}

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
	mergedFacts := s.mergeFacts(diskFacts, s.Facts, s.lastModTime)
	s.Facts = mergedFacts
	s.lastModTime = stat.ModTime()

	log.Printf("SyncFromDisk: ディスクから同期完了 (%d件)", len(s.Facts))
	return nil
}

// syncFromDiskUnsafe loads checks disk state and merges updates WITHOUT internal locking.
// Caller MUST hold s.mu and s.fileLock.
func (s *FactStore) syncFromDiskUnsafe() error {
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

	// ディスクから読み込み
	data, err := os.ReadFile(s.saveFilePath)
	if err != nil {
		return err
	}

	var diskFacts []model.Fact
	if err := json.Unmarshal(data, &diskFacts); err != nil {
		return fmt.Errorf("failed to unmarshal facts: %w", err)
	}

	// マージ実行
	// メモリ上のデータとディスク上のデータをマージ（ディスク優先）
	mergedFacts := s.mergeFacts(diskFacts, s.Facts, s.lastModTime)
	s.Facts = mergedFacts
	s.lastModTime = stat.ModTime()

	log.Printf("syncFromDiskUnsafe: ディスクから同期完了 (Total: %d)", len(s.Facts))
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

// Reload discards in-memory facts older than the cutoff time and reloads from disk.
// This is used to refresh the state after a long delay (e.g., startup), preserving
// only the facts that were newly added during that delay.
func (s *FactStore) Reload(cutoff time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. ディスクから最新をロード
	var diskFacts []model.Fact
	data, err := os.ReadFile(s.saveFilePath)
	if err == nil {
		if err := json.Unmarshal(data, &diskFacts); err != nil {
			return fmt.Errorf("failed to parse facts from disk: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read facts from disk: %w", err)
	}

	// 2. メモリ上の「新しい」ファクトのみを抽出
	var newMemoryFacts []model.Fact
	for _, f := range s.Facts {
		if f.Timestamp.After(cutoff) {
			newMemoryFacts = append(newMemoryFacts, f)
		}
	}

	// 3. マージ（ディスク優先、ただし新しいメモリファクトは追加）
	// mergeFactsは内部で重複チェックを行う
	mergedFacts := s.mergeFacts(diskFacts, newMemoryFacts, s.lastModTime)

	s.Facts = mergedFacts

	// Update lastModTime to matches disk if we loaded successfully
	if stat, err := os.Stat(s.saveFilePath); err == nil {
		s.lastModTime = stat.ModTime()
	}

	log.Printf("Reload完了: ディスク(%d) + 新規メモリ(%d) -> 統合(%d)",
		len(diskFacts), len(newMemoryFacts), len(s.Facts))

	return nil
}
