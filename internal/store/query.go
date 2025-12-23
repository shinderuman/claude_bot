package store

import (
	"fmt"
	"log"
	"strings"
	"time"

	"claude_bot/internal/model"
)

// SearchFuzzy はファクトの曖昧検索を行います
func (s *FactStore) SearchFuzzy(targets []string, keys []string) []model.Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []model.Fact
	for _, fact := range s.Facts {
		// Targetの一致確認
		targetMatch := false
		for _, t := range targets {
			// 完全一致は常にチェック（TargetもTargetUserNameも）
			if fact.Target == t || fact.TargetUserName == t {
				targetMatch = true
				break
			}
			// クエリがN文字以上の場合のみ、前方一致・後方一致もチェック（TargetUserNameのみ）
			if len(t) >= MinTargetUserNameFuzzyLength {
				if strings.HasPrefix(fact.TargetUserName, t) || strings.HasSuffix(fact.TargetUserName, t) {
					targetMatch = true
					break
				}
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

			if strings.HasPrefix(fact.Key, "system:") {
				valStr := fmt.Sprintf("%v", fact.Value)
				if strings.Contains(valStr, key) {
					results = append(results, fact)
					break
				}
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

	// 末尾からlimit件取得 (TODO: 厳密な時系列順が必要ならソート実装)
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

// GetRandomGeneralFactBundle は同じ情報源から最大limit件のランダムなファクトを取得します
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

	// ランダムに情報源を選択
	if len(sources) == 0 {
		return nil, nil
	}

	idx := int(time.Now().UnixNano() % int64(len(sources)))
	selectedSource := sources[idx]
	selectedFacts := sourceMap[selectedSource]

	// 4. 選択されたファクトから最大limit件を取得
	if len(selectedFacts) <= limit {
		return selectedFacts, nil
	}

	// シャッフル（Fisher-Yates）して先頭から選ぶ
	shuffled := make([]model.Fact, len(selectedFacts))
	copy(shuffled, selectedFacts)

	for i := len(shuffled) - 1; i > 0; i-- {
		j := int(time.Now().UnixNano() % int64(i+1))
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return shuffled[:limit], nil
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
