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

// SetErrorNotifier sets the callback function for error notifications.
func SetErrorNotifier(notifier ErrorNotifierFunc) {
	errorNotifier = notifier
}

// RepairJSON attempts to repair a truncated or malformed JSON string.
// Repairs unclosed arrays and stack-based structural issues.
func RepairJSON(s string) string {
	s = preprocessJSON(s)
	s = strings.TrimSpace(s)

	// Strategy 0: Double Array check
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		s = s[1 : len(s)-1]
	}

	// Strategy 1: Truncated Top-Level Array Repair
	// Apply if string starts with '[' but doesn't end with ']'.
	if strings.HasPrefix(s, "[") && !strings.HasSuffix(s, "]") {
		lastObjEnd := strings.LastIndex(s, "}")
		if lastObjEnd != -1 && lastObjEnd < len(s)-1 {
			return s[:lastObjEnd+1] + "]"
		}
		// Fallback to empty array if no object end found.
		if lastObjEnd == -1 {
			return "[]"
		}
	}

	// Strategy 2: Structural Repair (Stack-based)
	var sb strings.Builder
	var stack []rune // Stack: '{' or '['
	inString := false
	escaped := false

	// Convert to rune slice for lookahead.
	runes := []rune(s)

	// Helper to get stack top.
	peek := func() rune {
		if len(stack) == 0 {
			return 0
		}
		return stack[len(stack)-1]
	}

	// Scan and repair inline mismatches.
	for i, r := range runes {

		// Handle string literals.
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
			if inString {
				// Check for closing quote via lookahead to structural delimiters.
				// Next char must be delimiter (EOF counts as closing).
				isClosing := true
				for j := i + 1; j < len(runes); j++ {
					next := runes[j]
					if next == ' ' || next == '\t' || next == '\r' || next == '\n' {
						continue
					}
					// If next char is NOT delimiter, quote is internal.
					if next != ',' && next != '}' && next != ']' && next != ':' {
						isClosing = false
					}
					break
				}

				if isClosing {
					inString = !inString
					sb.WriteRune(r)
				} else {
					// Internal quote: escape it.
					sb.WriteString(`\"`)
				}
			} else {
				// Start of string.
				inString = true
				sb.WriteRune(r)
			}
			continue
		}

		if inString {
			// Infer unclosed string if structural closer matches stack.
			closing := false
			if (r == '}' && peek() == '{') || (r == ']' && peek() == '[') {
				closing = true
			}

			if closing {
				inString = false
				sb.WriteRune('"')
				// Handle closer as structure.
			} else {
				// Regular string content.
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
		}

		// Structure handling
		switch r {
		case '{', '[':
			stack = append(stack, r)
			sb.WriteRune(r)
		case '}':
			// If stack top is '[', insert missing ']' -> '}'
			if peek() == '[' {
				sb.WriteRune(']')
				stack = stack[:len(stack)-1] // Pop '['
				if peek() == '{' {
					stack = stack[:len(stack)-1] // Pop '{'
					sb.WriteRune('}')
				}
			} else if peek() == '{' {
				stack = stack[:len(stack)-1] // Pop '{'
				sb.WriteRune('}')
			} else {
				// Validation mismatch: write conservatively
				sb.WriteRune('}')
			}
			// If we closed the root element, we're done.
			if len(stack) == 0 {
				return sb.String()
			}
		case ']':
			// If stack top is '{', insert missing '}' -> ']'
			if peek() == '{' {
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
			// If we closed the root element, we're done.
			if len(stack) == 0 {
				return sb.String()
			}
		default:
			sb.WriteRune(r)
		}
	}

	// Close open string if necessary
	if inString {
		sb.WriteRune('"')
	}

	// Close remaining open brackets/braces
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

// preprocessJSON normalizes formatting issues like full-width characters.
func preprocessJSON(s string) string {
	// 1. Replace full-width colons
	s = strings.ReplaceAll(s, "：", ":")

	// 2. Add quotes to keys: "key: value" -> "key": "value"
	// Guard against replacing protocols (e.g., http:).
	reKeyFix := regexp.MustCompile(`([,{]\s*)"([a-zA-Z0-9_]+):`)
	s = reKeyFix.ReplaceAllString(s, `$1"$2":`)

	// 3. Normalize Japanese quotes 「...」 to "..."
	// Replace opening quote
	reStart := regexp.MustCompile(`:\s*「`)
	s = reStart.ReplaceAllString(s, `: "`)

	// Replace closing quote if followed by delimiter
	reEnd := regexp.MustCompile(`」(\s*[\}\],])`)
	s = reEnd.ReplaceAllString(s, `"$1`)

	// 4. Fix missing closing quote before comma: "value,"key"
	reMissingQuote := regexp.MustCompile(`:"([a-zA-Z0-9_]+),"([a-zA-Z0-9_]+)":`)
	s = reMissingQuote.ReplaceAllString(s, `:"$1","$2":`)

	// 5. Fix garbage quotes at end of value: "value""}
	// Exclude valid empty strings.
	reDoubleQuote := regexp.MustCompile(`([^:,\s\[\{])""(\s*[\}\],])`)
	s = reDoubleQuote.ReplaceAllString(s, `$1"$2`)

	return s
}

func IsDoubleArray(s string) bool {
	return strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]")
}

// UnmarshalWithRepair attempts unmarshal; retries with repair on failure.
// Logs detailed error only if repair also fails.
func UnmarshalWithRepair(jsonStr string, v interface{}, logPrefix string) error {
	if err := json.Unmarshal([]byte(jsonStr), v); err != nil {
		// Retry with repair
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
