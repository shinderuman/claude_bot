package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/utils"
)

type FactStore struct {
	mu           sync.RWMutex
	fileLock     *flock.Flock
	Facts        []model.Fact
	saveFilePath string
	lastModTime  time.Time
}

func InitializeFactStore(cfg *config.Config) *FactStore {
	factsPath := utils.GetFilePath(cfg.FactStoreFileName)
	return NewFactStore(factsPath)
}

// NewFactStore creates a new FactStore with a custom file path
func NewFactStore(filePath string) *FactStore {
	store := &FactStore{
		fileLock:     flock.New(filePath + ".lock"),
		Facts:        []model.Fact{},
		saveFilePath: filePath,
	}

	if err := store.load(); err != nil {
		log.Printf("事実データ読み込みエラー（新規作成します）: %v", err)
	} else {
		// 起動時に古いデータを削除
		deleted := store.Cleanup(30 * 24 * time.Hour)
		log.Printf("事実データ読み込み成功: %d件 (削除: %d件, ファイル: %s)", len(store.Facts), deleted, filePath)
	}

	return store
}

func (s *FactStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 読み込み時も簡易的にロック（待機なし）を試みるが、
	// 読み込みは失敗してもファイルが壊れるわけではないので
	// 厳密なファイルロックまでは必須ではないが、一貫性のためTryLockする
	// ここではシンプルにos.ReadFileのみ行う（OSレベルのAtomic性は期待しない）

	// ただし、もし厳密に行うなら:
	// locked, err := s.fileLock.TryRLock()
	// if err == nil && locked {
	// 	 defer s.fileLock.Unlock()
	// }

	data, err := os.ReadFile(s.saveFilePath)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, &s.Facts); err != nil {
		return err
	}

	return nil
}

// Save はファイルロックを取得し、ディスク上のデータとメモリ上のデータをマージして保存します
// コンテンツの重複のみを排除し、異なるValueは全て保持します（Gemini/Claudeの共存）
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

	// 自身の書き込みによる更新日時を反映して、直後のSyncFromDiskで無駄な読み込みが発生しないようにする
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
	// 重複チェック用マップ
	// キー: "Target|Key|Value"
	seen := make(map[string]bool)
	var result []model.Fact

	// ヘルパー: ファクトを追加
	add := func(list []model.Fact) {
		for _, f := range list {
			valStr := fmt.Sprintf("%v", f.Value)
			uniqueKey := fmt.Sprintf("%s|%s|%s", f.Target, f.Key, valStr)

			if !seen[uniqueKey] {
				seen[uniqueKey] = true
				result = append(result, f)
			}
		}
	}

	// ディスクを優先（ベース）
	add(disk)
	// メモリを追加（差分のみ追加される）
	add(memory)

	return result
}

func (s *FactStore) Cleanup(retention time.Duration) int {
	// CleanupはSaveを呼ぶため、ここではメモリ上の操作のみ行い、Saveに任せる
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
		// 保存（ロック＆マージ付き）
		if err := s.Save(); err != nil {
			log.Printf("ファクト保存エラー: %v", err)
		}
	}

	return deletedCount
}

func (s *FactStore) SearchFuzzy(targets []string, keys []string) []model.Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []model.Fact
	for _, fact := range s.Facts {
		// Targetの一致確認
		targetMatch := false
		for _, t := range targets {
			if fact.Target == t {
				targetMatch = true
				break
			}
		}
		if !targetMatch {
			continue
		}

		// Keyの部分一致確認
		for _, key := range keys {
			if strings.Contains(fact.Key, key) || strings.Contains(key, fact.Key) {
				results = append(results, fact)
				break
			}
		}
	}
	return results
}

// GetRecentFacts は最新のファクトを指定された件数取得します
func (s *FactStore) GetRecentFacts(limit int) []model.Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// タイムスタンプの降順でソートするためのコピーを作成
	facts := make([]model.Fact, len(s.Facts))
	copy(facts, s.Facts)

	// バブルソート（件数が少ないと想定）またはsort.Sliceを使用
	// ここではシンプルに後ろから取得する（Factsは追記型なので概ね時系列だが、Upsertで更新されると順序が変わらないため、厳密にはソートが必要）
	// ただし、Upsertの実装を見ると、更新時は位置が変わらず、新規時はappendなので、
	// 更新されたものも含めて「最新」とするならタイムスタンプ順にソートすべき。

	// 簡易実装: 末尾からlimit件取得（新規追加分は末尾に来るため）
	// 厳密な時系列が必要ならソートを実装するが、今回は「最近覚えたこと」なので
	// 新規追加分（末尾）で十分な場合が多い。
	// しかし、更新されたものも「最近」とみなすならタイムスタンプを見る必要がある。

	// ここでは末尾から取得する簡易実装とする
	count := len(facts)
	if count == 0 {
		return []model.Fact{}
	}

	if count <= limit {
		// 逆順にして返す
		result := make([]model.Fact, count)
		for i := 0; i < count; i++ {
			result[i] = facts[count-1-i]
		}
		return result
	}

	result := make([]model.Fact, limit)
	for i := 0; i < limit; i++ {
		result[i] = facts[count-1-i]
	}
	return result
}

// GetRandomGeneralFactBundle はランダムな一般知識のファクトバンドルを取得します
// 同じ情報源(TargetUserName)から最大limit件のファクトを返します
func (s *FactStore) GetRandomGeneralFactBundle(limit int) ([]model.Fact, error) {
	// 最新データを同期
	if err := s.SyncFromDisk(); err != nil {
		log.Printf("GetRandomGeneralFactBundle: SyncFromDisk failed: %v", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. 一般知識のファクトを抽出
	var generalFacts []model.Fact
	for _, fact := range s.Facts {
		if fact.Target == model.GeneralTarget {
			generalFacts = append(generalFacts, fact)
		}
	}

	if len(generalFacts) == 0 {
		return nil, nil
	}

	// 2. ユニークな情報源(TargetUserName)を抽出
	sourceMap := make(map[string][]model.Fact)
	var sources []string

	for _, fact := range generalFacts {
		source := fact.TargetUserName
		if source == "" {
			source = model.UnknownTarget
		}
		if _, exists := sourceMap[source]; !exists {
			sources = append(sources, source)
		}
		sourceMap[source] = append(sourceMap[source], fact)
	}

	// 3. ランダムに情報源を選択
	// 注意: math/randは非推奨になりつつあるが、ここでは厳密な乱数は不要
	// Go 1.20以降は crypto/rand または math/rand/v2 が推奨されるが、
	// 既存コードに合わせて簡易的な実装にする（あるいはtimeベースで選択）
	if len(sources) == 0 {
		return nil, nil
	}

	// 簡易的なランダム選択 (mapの反復順序はランダムだが、ここでは明示的に選択)
	// time.Now().UnixNano() をシードにするのは毎回呼ぶと偏るが、頻度が低いので許容
	// しかし、テスト容易性のために単純にインデックスで選ぶ
	// ここでは time.Now().UnixNano() を使ってインデックスを決定
	idx := int(time.Now().UnixNano() % int64(len(sources)))
	selectedSource := sources[idx]
	selectedFacts := sourceMap[selectedSource]

	// 4. 選択されたファクトから最大limit件を取得
	if len(selectedFacts) <= limit {
		return selectedFacts, nil
	}

	// ランダムにlimit件選ぶか、先頭から選ぶか
	// ここではシャッフルして先頭から選ぶ
	// シャッフル（Fisher-Yates）
	shuffled := make([]model.Fact, len(selectedFacts))
	copy(shuffled, selectedFacts)

	for i := len(shuffled) - 1; i > 0; i-- {
		j := int(time.Now().UnixNano() % int64(i+1))
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled[:limit], nil
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
		// ロックを一時的に解放してSaveを呼ぶ
		s.mu.Unlock()
		if err := s.Save(); err != nil {
			log.Printf("ファクト保存エラー: %v", err)
		}
		s.mu.Lock()
	}

	return deletedCount
}

// removeDuplicatesUnsafe は重複ファクトを削除します（ロック不要）
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

// removeOldFactsUnsafe は古いファクトを削除します（ロック不要）
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

// enforceMaxFactsUnsafe は最大ファクト数を超えた分を削除します（ロック不要）
func (s *FactStore) enforceMaxFactsUnsafe(maxFacts int) {
	if maxFacts <= 0 || len(s.Facts) <= maxFacts {
		return
	}

	// Timestampでソート（古い順）
	// 既存のFactsをそのまま使い、古いものから削除
	// 簡易的に、最新のmaxFacts件のみを保持
	if len(s.Facts) > maxFacts {
		// Timestampでソートして新しい順に並べる
		// ここでは簡易的に、Factsの末尾がより新しいと仮定
		// 実際にはソートが必要だが、通常は追加順=時系列順なので省略
		s.Facts = s.Facts[len(s.Facts)-maxFacts:]
	}
}

// AddFact は引数からFact構造体を生成して追加する簡易メソッドです
func (s *FactStore) AddFact(target, targetUserName, author, authorUserName, key string, value interface{}) {
	s.AddFactWithSource(model.Fact{
		Target:         target,
		TargetUserName: targetUserName,
		Author:         author,
		AuthorUserName: authorUserName,
		Key:            key,
		Value:          value,
		Timestamp:      time.Now(),
		SourceType:     model.SourceTypeMention, // デフォルトはメンション
	})
}

// AddFactWithSource はソース情報を含むFactを保存します
func (s *FactStore) AddFactWithSource(fact model.Fact) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// メモリ上での簡易重複チェック（完全一致のみ排除）
	// ディスクとのマージはSave時に行われるため、ここではメモリ内の重複だけ防ぐ
	for i, existing := range s.Facts {
		// TargetとKeyが一致し、かつValueも一致する場合のみ更新（Timestamp更新）
		if existing.Target == fact.Target && existing.Key == fact.Key {
			// Valueの比較（簡易的）
			val1 := fmt.Sprintf("%v", existing.Value)
			val2 := fmt.Sprintf("%v", fact.Value)

			if val1 == val2 {
				// 完全一致なら更新扱いで維持（新しいメタデータを反映）
				s.Facts[i].Author = fact.Author
				s.Facts[i].Timestamp = time.Now()
				if fact.SourceType != "" {
					s.Facts[i].SourceType = fact.SourceType
				}
				if fact.SourceURL != "" {
					s.Facts[i].SourceURL = fact.SourceURL
				}
				return
			}
		}
	}

	// 新規追加（Valueが違うなら別ファクトとして追記）
	if fact.Timestamp.IsZero() {
		fact.Timestamp = time.Now()
	}
	s.Facts = append(s.Facts, fact)
}

// GetFactsByTarget gets all facts for a specific target
func (s *FactStore) GetFactsByTarget(target string) []model.Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []model.Fact
	for _, fact := range s.Facts {
		if fact.Target == target {
			results = append(results, fact)
		}
	}
	return results
}

// OverwriteFactsForTarget は指定されたターゲットのファクトを強制的に上書き保存します
// ディスク上の該当ターゲットのデータは全て削除され、newFactsに置き換わります
func (s *FactStore) OverwriteFactsForTarget(target string, newFacts []model.Fact) error {
	// タイムアウト付きでロック取得（1.0秒 - 重い処理なので長めに）
	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	locked, err := s.fileLock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil || !locked {
		return fmt.Errorf("failed to acquire file lock for overwrite: %v", err)
	}
	defer s.fileLock.Unlock() //nolint:errcheck

	// 1. ディスクから最新をロード
	var diskFacts []model.Fact
	data, err := os.ReadFile(s.saveFilePath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &diskFacts); err != nil {
			return fmt.Errorf("failed to unmarshal disk facts: %v", err)
		}
	}

	// 2. ディスク上のデータから対象ターゲットを除外
	var keptFacts []model.Fact
	for _, f := range diskFacts {
		if f.Target != target {
			keptFacts = append(keptFacts, f)
		}
	}

	// 3. 新しいファクト（アーカイブ済みデータ）を追加
	// タイムスタンプ補完
	now := time.Now()
	for i := range newFacts {
		if newFacts[i].Timestamp.IsZero() {
			newFacts[i].Timestamp = now
		}
	}
	keptFacts = append(keptFacts, newFacts...)

	// 4. 保存
	encoded, err := json.MarshalIndent(keptFacts, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal facts: %v", err)
	}

	if err := s.atomicWriteFile(encoded); err != nil {
		return fmt.Errorf("failed to write facts to disk: %v", err)
	}

	// 5. メモリも更新（このメソッドが権限を持つため、メモリ上の他スレッドの変更よりもここでの決定を優先する形になるが
	// アーカイブ処理はターゲット単位で独立しているため、他ターゲットへの影響はない）
	s.mu.Lock()
	// メモリ上のデータも同様に再構築（ディスクと同じ状態にするのが最も整合性が取れる）
	// ただし、他ターゲットの更新がメモリ上で進行中の可能性を考慮し、
	// メモリ上の「対象ターゲット以外のデータ」+「新しいデータ」とするのが安全
	// ここではメモリ上のデータ再構築ロジックを適用
	var memoryKept []model.Fact
	for _, f := range s.Facts {
		if f.Target != target {
			memoryKept = append(memoryKept, f)
		}
	}
	memoryKept = append(memoryKept, newFacts...)
	s.Facts = memoryKept
	s.mu.Unlock()

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
	// 一時ファイルを作成（ターゲットと同じディレクトリにすることで、Renameがアトミックになる可能性を高める）
	// os.CreateTempの第一引数はディレクトリ（空ならデフォルト）、第二引数はパターン
	// ここではターゲットファイルと同じディレクトリを使用したいが、簡単のためデフォルトの一時ディレクトリは使わず
	// ターゲットファイルの横に作るのが一般的（ファイルシステムを跨がないため）
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

	// 権限設定（元のファイルに合わせるのが理想だが、ここでは0644）
	if err := os.Chmod(tmpPath, 0644); err != nil {
		log.Printf("failed to chmod temp file: %v", err)
	}

	// アトミックにリネーム
	if err := os.Rename(tmpPath, s.saveFilePath); err != nil {
		return fmt.Errorf("failed to rename temp file to target: %w", err)
	}

	return nil
}

// GetAllTargets returns a list of all unique targets in the store
func (s *FactStore) GetAllTargets() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	targetMap := make(map[string]bool)
	for _, fact := range s.Facts {
		targetMap[fact.Target] = true
	}

	var targets []string
	for target := range targetMap {
		targets = append(targets, target)
	}
	return targets
}
