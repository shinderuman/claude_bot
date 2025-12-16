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

	// Double Array check: strictly [[...]] format
	// If it starts with [[ and ends with ]], remove the outer brackets
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		s = s[1 : len(s)-1]
	}

	// 配列で始まっていない場合は対象外（あるいは既に単純な文字列など）
	if !strings.HasPrefix(s, "[") {
		return s
	}

	// Escape control characters (newlines, etc.) within string literals
	// This uses a simple state machine to detect if we are inside quotes
	var sb strings.Builder
	inString := false
	escaped := false
	for _, r := range s {
		if escaped {
			sb.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' {
			escaped = true
			sb.WriteRune(r)
			continue
		}

		if r == '"' {
			inString = !inString
			sb.WriteRune(r)
			continue
		}

		if inString {
			switch r {
			case '\n':
				sb.WriteString("\\n")
			case '\r':
				// ignore or escape? let's escape to \r just in case, or ignore.
				// JSON spec: \r is also not allowed in strings.
				sb.WriteString("\\r")
			case '\t':
				// raw tabs are often allowed by some parsers but strictly invalid in strings?
				// Actually unescaped tab IS invalid in JSON strings (0x00-0x1F are invalid).
				sb.WriteString("\\t")
			default:
				sb.WriteRune(r)
			}
		} else {
			sb.WriteRune(r)
		}
	}
	s = sb.String()

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
