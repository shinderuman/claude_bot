package llm

import (
	"encoding/json"
	"log"
	"strings"
)

// RepairJSON attempts to repair a truncated JSON string.
// Currently, it specifically targets JSON arrays that have been cut off.
// It tries to find the last valid object closing "}" and appends "]" to close the array.
func RepairJSON(s string) string {
	s = strings.TrimSpace(s)

	// 配列で始まっていない場合は対象外（あるいは既に単純な文字列など）
	if !strings.HasPrefix(s, "[") {
		return s
	}

	// 既に閉じている場合はそのまま返す
	if strings.HasSuffix(s, "]") {
		return s
	}

	// 最後の "}" を探す
	lastObjEnd := strings.LastIndex(s, "}")
	if lastObjEnd == -1 {
		// オブジェクトが一つも見つからない場合は空配列を返す（リスク回避）
		return "[]"
	}

	// 最後の "}" まで切り取り、"]" を付与
	// これにより、途中で切れた最後の要素は捨てられる
	repaired := s[:lastObjEnd+1] + "]"
	return repaired
}

// UnmarshalWithRepair tries to unmarshal JSON. If it fails, it attempts to repair the JSON and tries again.
// It logs a simple error on the first failure, and a detailed error (with JSON) only if the repair also fails.
func UnmarshalWithRepair(jsonStr string, v interface{}, logPrefix string) error {
	if err := json.Unmarshal([]byte(jsonStr), v); err != nil {
		// リトライ: JSON修復を試みる
		repairedJSON := RepairJSON(jsonStr)
		if err := json.Unmarshal([]byte(repairedJSON), v); err != nil {
			log.Printf("%sJSONパースエラー(修復後): %v\nOriginal: %s\nRepaired: %s", logPrefix, err, jsonStr, repairedJSON)
			return err
		}
	}
	return nil
}
