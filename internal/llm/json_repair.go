package llm

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
)

// ErrorNotifierFunc defines the callback signature for error notifications
type ErrorNotifierFunc func(message, details string)

var errorNotifier ErrorNotifierFunc

// SetErrorNotifier sets the callback function for error notifications
func SetErrorNotifier(notifier ErrorNotifierFunc) {
	errorNotifier = notifier
}

// RepairJSON attempts to repair a truncated or malformed JSON string.
// It uses a hybrid strategy:
// 1. For unclosed top-level arrays, it truncates to the last valid object.
// 2. For others (objects, nested issues), it uses a stack-based structural repair.
func RepairJSON(s string) string {
	s = preprocessJSON(s)
	s = strings.TrimSpace(s)

	// Strategy 0: Double Array check (Legacy support)
	// If it starts with [[ and ends with ]], remove the outer brackets
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		s = s[1 : len(s)-1]
	}

	// Strategy 1: Truncated Top-Level Array Repair
	// Apply ONLY if it looks like a standard array that got cut off.
	// Condition: Starts with '[' AND does NOT end with ']'
	if strings.HasPrefix(s, "[") && !strings.HasSuffix(s, "]") {
		lastObjEnd := strings.LastIndex(s, "}")
		if lastObjEnd != -1 {
			return s[:lastObjEnd+1] + "]"
		}
		// If no '}' found, risk returning empty array
		return "[]"
	}

	// Strategy 2: Structural Repair (Stack-based)
	// Handles objects, closed arrays with internal issues, etc.
	var sb strings.Builder
	var stack []rune // Stack of open brackets/braces: '{' or '['
	inString := false
	escaped := false

	// Helper to get stack top
	peek := func() rune {
		if len(stack) == 0 {
			return 0
		}
		return stack[len(stack)-1]
	}

	// First pass: scanning and repairing inline mismatches
	for _, r := range s {
		// Handle string literals
		if escaped {
			sb.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			sb.WriteRune(r)
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			sb.WriteRune(r)
			continue
		}
		if inString {
			// Escape control characters if specific cases need it (reusing logic from previous impl if needed)
			// But for simplicity in this rewrite, we assume input string content is mostly valid or we fix minimal escapes.
			// Let's reuse the newline escape logic from previous impl to be safe.
			switch r {
			case '\n':
				sb.WriteString("\\n")
			case '\r':
				sb.WriteString("\\r")
			case '\t':
				sb.WriteString("\\t")
			default:
				sb.WriteRune(r)
			}
			continue
		}

		// Structure handling
		switch r {
		case '{', '[':
			stack = append(stack, r)
			sb.WriteRune(r)
		case '}':
			// If stack top is '[', we are missing a ']'
			if peek() == '[' {
				// Insert missing ']' before '}'
				sb.WriteRune(']')
				stack = stack[:len(stack)-1] // Pop '['
				// Now handle the '}' again (recurse logic technically, or just proceed)
				// Check if stack is now empty or has '{'
				if peek() == '{' {
					stack = stack[:len(stack)-1] // Pop '{'
				}
				sb.WriteRune('}')
			} else if peek() == '{' {
				stack = stack[:len(stack)-1] // Pop '{'
				sb.WriteRune('}')
			} else {
				// Stack empty or mismatch, ignore extra '}' or treat as error?
				// Treating as valid closing of potential implicit root?
				// For robustness, if stack is empty, we might ignore, or just append.
				sb.WriteRune('}')
			}
		case ']':
			// If stack top is '{', we are missing a '}' -> Invalid JSON mostly, but let's try to fix
			if peek() == '{' {
				// Insert missing '}' before ']'
				sb.WriteRune('}')
				stack = stack[:len(stack)-1] // Pop '{'

				if peek() == '[' {
					stack = stack[:len(stack)-1] // Pop '['
				}
				sb.WriteRune(']')
			} else if peek() == '[' {
				stack = stack[:len(stack)-1] // Pop '['
				sb.WriteRune(']')
			} else {
				sb.WriteRune(']')
			}
		default:
			sb.WriteRune(r)
		}
	}

	// Post-processing: Close any remaining open brackets/braces
	for i := len(stack) - 1; i >= 0; i-- {
		switch stack[i] {
		case '{':
			sb.WriteRune('}')
		case '[':
			sb.WriteRune(']')
		}
	}

	return sb.String()
}

// preprocessJSON handles common LLM formatting issues like full-width characters
func preprocessJSON(s string) string {
	// 1. Replace all full-width colons with half-width
	// Note: This might replace colons inside strings, but for "Repair" purposes
	// it's a necessary compromise if the structure is broken.
	s = strings.ReplaceAll(s, "：", ":")

	// 2. Handle Japanese quotes 「...」 used as value delimiters
	// Pattern: : "..." is standard. : 「...」 is observed.
	// We want to replace start `：「` with `: "`
	// And end `」` with `"` if it's likely a closing quote (followed by comma, brace, or bracket)

	// Replace start tag
	reStart := regexp.MustCompile(`:\s*「`)
	s = reStart.ReplaceAllString(s, `: "`)

	// Replace end tag
	// Logic: replace `」` that is followed by `} `, `] `, `, ` or end of line (ignoring whitespace)
	reEnd := regexp.MustCompile(`」(\s*[\}\],])`)
	s = reEnd.ReplaceAllString(s, `"$1`)

	// Also handle trailing `」` at end of string if it looks like end of json?
	// But `RepairJSON` main logic handles structure. Let's just focus on delimiters.

	return s
}

func IsDoubleArray(s string) bool {
	return strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]")
}

// UnmarshalWithRepair tries to unmarshal JSON. If it fails, it attempts to repair the JSON and tries again.
// It logs a simple error on the first failure, and a detailed error (with JSON) only if the repair also fails.
func UnmarshalWithRepair(jsonStr string, v interface{}, logPrefix string) error {
	if err := json.Unmarshal([]byte(jsonStr), v); err != nil {
		// リトライ: JSON修復を試みる
		repairedJSON := RepairJSON(jsonStr)
		if err := json.Unmarshal([]byte(repairedJSON), v); err != nil {
			msg := fmt.Sprintf("%sJSONパースエラー(修復後): %v", logPrefix, err)
			detail := fmt.Sprintf("Original: %s\nRepaired: %s", jsonStr, repairedJSON)
			log.Printf("%s\n%s", msg, detail)

			if errorNotifier != nil {
				go errorNotifier(msg, detail)
			}
			return err
		}
	}
	return nil
}
