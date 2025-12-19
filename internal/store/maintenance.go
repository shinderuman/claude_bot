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

func (s *FactStore) Cleanup(retention time.Duration) int {
	s.mu.Lock()

	threshold := time.Now().Add(-retention)
	var activeFacts []model.Fact
	deletedCount := 0

	for _, fact := range s.Facts {
		if fact.Timestamp.After(threshold) {
			activeFacts = append(activeFacts, fact)
		} else {
			deletedCount++
		}
	}

	s.Facts = activeFacts
	s.mu.Unlock()

	if deletedCount > 0 {
		// SaveOverwriteでマージなし保存（削除を反映）
		if err := s.SaveOverwrite(); err != nil {
			log.Printf("ファクト保存エラー(Cleanup): %v", err)
		}
	}

	return deletedCount
}

// PerformMaintenance はファクトストアの総合的なメンテナンスを実行します
func (s *FactStore) PerformMaintenance(retentionDays, maxFacts int) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	initialCount := len(s.Facts)

	// 1. 重複排除
	s.removeDuplicatesUnsafe()

	// 2. 古いファクトの削除
	s.removeOldFactsUnsafe(retentionDays)

	// 3. 上限超過分の削除
	s.enforceMaxFactsUnsafe(maxFacts)

	deletedCount := initialCount - len(s.Facts)
	if deletedCount > 0 {
		log.Printf("ファクトメンテナンス完了: %d件削除 (残り: %d件)", deletedCount, len(s.Facts))
		// ロックを一時的に解放してSaveOverwriteを呼ぶ（マージせずに削除を反映）
		s.mu.Unlock()
		if err := s.SaveOverwrite(); err != nil {
			log.Printf("ファクト保存エラー: %v", err)
		}
		s.mu.Lock()
	}

	return deletedCount
}

// removeDuplicatesUnsafe は重複ファクトを削除します (ロック不要)
func (s *FactStore) removeDuplicatesUnsafe() {
	type factKey struct {
		Target string
		Key    string
		Value  string
	}

	seen := make(map[factKey]*model.Fact)
	unique := make([]model.Fact, 0, len(s.Facts))

	for i := range s.Facts {
		fact := &s.Facts[i]
		// Valueを文字列に変換して比較
		valueStr := ""
		if fact.Value != nil {
			if str, ok := fact.Value.(string); ok {
				valueStr = strings.TrimSpace(str)
			}
		}

		key := factKey{
			Target: fact.Target,
			Key:    fact.Key,
			Value:  valueStr,
		}

		if existing, exists := seen[key]; exists {
			// 既存のファクトより新しい場合は置き換え
			if fact.Timestamp.After(existing.Timestamp) {
				seen[key] = fact
			}
		} else {
			seen[key] = fact
		}
	}

	// ユニークなファクトのみを保持
	for _, fact := range seen {
		unique = append(unique, *fact)
	}

	s.Facts = unique
}

// removeOldFactsUnsafe は古いファクトを削除します (ロック不要)
func (s *FactStore) removeOldFactsUnsafe(retentionDays int) {
	if retentionDays <= 0 {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	filtered := make([]model.Fact, 0, len(s.Facts))

	for _, fact := range s.Facts {
		if fact.Timestamp.After(cutoff) {
			filtered = append(filtered, fact)
		}
	}

	s.Facts = filtered
}

// enforceMaxFactsUnsafe は最大ファクト数を超えた分を削除します (ロック不要)
func (s *FactStore) enforceMaxFactsUnsafe(maxFacts int) {
	if maxFacts <= 0 || len(s.Facts) <= maxFacts {
		return
	}

	if len(s.Facts) > maxFacts {
		// 最新のmaxFacts件のみを保持
		s.Facts = s.Facts[len(s.Facts)-maxFacts:]
	}
}

// ReplaceFacts atomically replaces all facts for the given target.
func (s *FactStore) ReplaceFacts(target string, factsToRemove, factsToAdd []model.Fact) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	// ファイルロック（書き込み用）
	locked, err := s.fileLock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !locked {
		return fmt.Errorf("failed to acquire file lock for replace: %v", err)
	}
	defer s.fileLock.Unlock() //nolint:errcheck

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. ディスクから最新を読み込み
	var diskFacts []model.Fact
	data, err := os.ReadFile(s.saveFilePath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &diskFacts); err != nil {
			return fmt.Errorf("failed to unmarshal disk facts: %v", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to load facts from disk: %v", err)
	}

	keptAuthMap := make(map[string]model.Fact) // 重複排除用マップ (UniqueKey -> Fact)

	// 2. ディスクデータのフィルタリング
	for _, f := range diskFacts {
		if f.Target == target {
			continue
		}
		key := f.ComputeUniqueKey()
		keptAuthMap[key] = f
	}

	// 3. メモリデータの統合
	for _, f := range s.Facts {
		if f.Target == target {
			continue
		}
		key := f.ComputeUniqueKey()
		if _, exists := keptAuthMap[key]; !exists {
			keptAuthMap[key] = f
		}
	}

	// 4. 新規追加分（アーカイブ結果）を追加
	for _, f := range factsToAdd {
		key := f.ComputeUniqueKey()
		if f.Timestamp.IsZero() {
			f.Timestamp = time.Now()
		}
		keptAuthMap[key] = f
	}

	// リスト化
	var finalFacts []model.Fact
	for _, f := range keptAuthMap {
		finalFacts = append(finalFacts, f)
	}

	// 5. 保存 (Atomic Write)
	encoded, err := json.MarshalIndent(finalFacts, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal facts: %v", err)
	}

	if err := s.atomicWriteFile(encoded); err != nil {
		return fmt.Errorf("failed to write facts to disk (atomic): %v", err)
	}

	// 6. メモリ更新
	s.Facts = finalFacts
	s.lastModTime = time.Now()

	return nil
}

// RemoveFacts removes facts matching the condition and persists changes immediately via Atomic Update
func (s *FactStore) RemoveFacts(ctx context.Context, target string, shouldRemove func(model.Fact) bool) (int, error) {
	// 1. Acquire File Lock FIRST to prevent concurrent Saves/Reads
	// This ensures no one reads the "dirty" state while we are deleting
	flockCtx, cancel := context.WithTimeout(ctx, 2*time.Second) // Longer timeout for maintenance
	defer cancel()

	locked, err := s.fileLock.TryLockContext(flockCtx, 100*time.Millisecond)
	if err != nil || !locked {
		return 0, fmt.Errorf("failed to acquire file lock for removal: %v", err)
	}
	defer s.fileLock.Unlock() //nolint:errcheck

	s.mu.Lock()
	defer s.mu.Unlock()

	// 2. Refresh from disk to ensure we are deleting from latest state
	// (Though Reload handles startup, redundant safety here is good)
	if err := s.syncFromDiskUnsafe(); err != nil {
		log.Printf("[RemoveFacts] Warning: Failed to sync from disk before removal: %v", err)
	}

	initialCount := len(s.Facts)
	newFacts := make([]model.Fact, 0, initialCount)
	deletedCount := 0

	for _, fact := range s.Facts {
		// ターゲットが一致し、かつ条件に合致する場合は削除対象（newFactsに追加しない）
		if fact.Target == target && shouldRemove(fact) {
			jsonBytes, _ := json.Marshal(fact)
			log.Printf("🗑️ ファクト削除: %s", string(jsonBytes))

			jsonIndentBytes, _ := json.MarshalIndent(fact, "", "    ")
			msg := fmt.Sprintf("🗑️ ファクトを削除しました (Target: %s)\n```\n%s\n```", target, string(jsonIndentBytes))
			s.slackClient.PostMessageAsync(ctx, msg)

			deletedCount++
			continue
		}
		newFacts = append(newFacts, fact)
	}

	if deletedCount > 0 {
		s.Facts = newFacts

		// 3. Save directly (Atomic Write)
		data, err := json.MarshalIndent(s.Facts, "", "  ")
		if err != nil {
			return deletedCount, fmt.Errorf("failed to marshal facts: %v", err)
		}

		if err := s.atomicWriteFile(data); err != nil {
			return deletedCount, fmt.Errorf("failed to write facts to disk: %w", err)
		}

		// Update timestamp
		if stat, err := os.Stat(s.saveFilePath); err == nil {
			s.lastModTime = stat.ModTime()
		}

		log.Printf("RemoveFacts: ターゲット %s から %d 件のファクトを削除しました", target, deletedCount)
	}

	return deletedCount, nil
}
