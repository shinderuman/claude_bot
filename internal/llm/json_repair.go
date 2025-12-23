package llm

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
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

// UnmarshalWithRepair attempts unmarshal; retries with repair on failure.
// Logs detailed error only if repair also fails.
func UnmarshalWithRepair(jsonStr string, v interface{}, logPrefix string) error {
	if err := json.Unmarshal([]byte(jsonStr), v); err != nil {
		repairedJSON := RepairJSON(jsonStr)
		if err := json.Unmarshal([]byte(repairedJSON), v); err != nil {
			if typeErr, ok := err.(*json.UnmarshalTypeError); ok {
				if typeErr.Type.Kind() == reflect.Slice && typeErr.Value == "object" {
					arrayWrapped := "[" + repairedJSON + "]"
					if err := json.Unmarshal([]byte(arrayWrapped), v); err == nil {
						return nil
					}
				}
			}

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

// RepairJSON attempts to repair a truncated or malformed JSON string.
// Repairs unclosed arrays and stack-based structural issues.
func RepairJSON(s string) string {
	s, originals := preprocessJSON(s)
	s = strings.TrimSpace(s)

	s = repairDoubleArray(s)
	s = repairTruncatedArray(s)
	s = repairStructural(s)
	s = fixDanglingKey(s)

	s = unmaskStrings(s, originals)
	return s
}

// preprocessJSON normalizes formatting.
func preprocessJSON(s string) (string, []string) {
	s = replaceFullWidthColons(s)
	s = unescapeSingleQuotes(s)
	s = fixMissingCommaQuotes(s)
	s = fixMergedKeyValue(s)
	s = fixHexEscapes(s)

	masked, originals := maskStrings(s)
	// Escape control characters
	for i, orig := range originals {
		orig = strings.ReplaceAll(orig, "\n", "\\n")
		orig = strings.ReplaceAll(orig, "\r", "\\r")
		orig = strings.ReplaceAll(orig, "\t", "\\t")
		originals[i] = orig
	}
	s = masked

	s = addQuotesToKeys(s)
	s = replaceOpeningJapaneseQuote(s)
	s = replaceClosingJapaneseQuote(s)
	s = fixMissingCommaBetweenValueAndKey(s)
	s = fixUnexpectedColon(s)
	s = fixInvalidKeyFormat(s)
	s = fixSemicolonSeparator(s)
	s = fixMissingCommaBetweenObjects(s)
	s = fixUnquotedValuesInArray(s)
	s = fixGarbageQuotes(s)
	s = fixMissingOpeningQuotes(s)
	s = fixGarbageKeyAfterObject(s)
	s = fixInvalidObjectToArray(s)
	s = removeTrailingCommas(s)

	return s, originals
}

func replaceFullWidthColons(s string) string {
	return strings.ReplaceAll(s, "：", ":")
}

func unescapeSingleQuotes(s string) string {
	return strings.ReplaceAll(s, `\'`, `'`)
}

func fixMissingCommaQuotes(s string) string {
	re := regexp.MustCompile(`:"([a-zA-Z0-9_]+),"([a-zA-Z0-9_]+)":`)
	return re.ReplaceAllString(s, `:"$1","$2":`)
}

func fixMergedKeyValue(s string) string {
	re := regexp.MustCompile(`([,{]\s*)"(value)([^":,]+)"`)
	return re.ReplaceAllString(s, `$1"$2":"$3"`)
}

func fixHexEscapes(s string) string {
	re := regexp.MustCompile(`\\x([0-9a-fA-F]{2})`)
	return re.ReplaceAllString(s, `\u00$1`)
}

func maskStrings(s string) (string, []string) {
	var originals []string
	re := regexp.MustCompile(`"[^"\\]*(?:\\.[^"\\]*)*"`)
	masked := re.ReplaceAllStringFunc(s, func(match string) string {
		placeholder := fmt.Sprintf(`"__STR_%d__"`, len(originals))
		originals = append(originals, match)
		return placeholder
	})
	return masked, originals
}

func addQuotesToKeys(s string) string {
	re := regexp.MustCompile(`([,{]\s*)"([a-zA-Z0-9_]+):`)
	return re.ReplaceAllString(s, `$1"$2":`)
}

func replaceOpeningJapaneseQuote(s string) string {
	re := regexp.MustCompile(`:\s*「`)
	return re.ReplaceAllString(s, `: "`)
}

func replaceClosingJapaneseQuote(s string) string {
	re := regexp.MustCompile(`」(\s*[\}\],])`)
	return re.ReplaceAllString(s, `"$1`)
}

func fixMissingCommaBetweenValueAndKey(s string) string {
	re := regexp.MustCompile(`("[^"]*")(\s+)("[a-zA-Z0-9_]+":)`)
	return re.ReplaceAllString(s, `$1,$2$3`)
}

func fixUnexpectedColon(s string) string {
	re := regexp.MustCompile(`(:\s*"(\\.|[^"\\])*")\s*:\s*`)
	return re.ReplaceAllString(s, `$1, "repaired_key":`)
}

func fixInvalidKeyFormat(s string) string {
	re := regexp.MustCompile(`"?([a-zA-Z0-9_]+)"?\s*=\s*`)
	return re.ReplaceAllString(s, `"$1":`)
}

func fixSemicolonSeparator(s string) string {
	re := regexp.MustCompile(`([}\]])\s*;\s*([{\[])`)
	return re.ReplaceAllString(s, `$1,$2`)
}

func fixMissingCommaBetweenObjects(s string) string {
	re := regexp.MustCompile(`([}\]])\s+([{\[])`)
	return re.ReplaceAllString(s, `$1,$2`)
}

func fixUnquotedValuesInArray(s string) string {
	re := regexp.MustCompile(`([\[,])\s*([^"\[\{\]\}\s,0-9\-tfn.][^"\[\{\]\}\s,]*)\s*([,\]])`)
	return re.ReplaceAllString(s, `$1"$2"$3`)
}

func fixGarbageQuotes(s string) string {
	re := regexp.MustCompile(`([^:,\s\[\{])""(\s*[\}\],])`)
	return re.ReplaceAllString(s, `$1"$2`)
}

func fixMissingOpeningQuotes(s string) string {
	re := regexp.MustCompile(`(:\s*)([^"\[\{\]\}\s0-9\-tfn\\])`)
	return re.ReplaceAllString(s, `$1"$2`)
}

func fixGarbageKeyAfterObject(s string) string {
	re := regexp.MustCompile(`}\s*([a-zA-Z0-9_]+)\s*:`)
	return re.ReplaceAllString(s, `, "$1":`)
}

func fixInvalidObjectToArray(s string) string {
	re := regexp.MustCompile(`\{\s*"[^"]+"(?:\s*,\s*"[^"]+")*\s*\}`)
	return re.ReplaceAllStringFunc(s, func(match string) string {
		return "[" + match[1:len(match)-1] + "]"
	})
}

func removeTrailingCommas(s string) string {
	re := regexp.MustCompile(`(["}\]el0-9])\s*,\s*([}\]])`)
	return re.ReplaceAllString(s, `$1$2`)
}

func repairDoubleArray(s string) string {
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		return s[1 : len(s)-1]
	}
	return s
}

func repairTruncatedArray(s string) string {
	// Apply if string starts with '[' but doesn't end with ']'.
	if strings.HasPrefix(s, "[") && !strings.HasSuffix(s, "]") {
		lastObjEnd := strings.LastIndex(s, "}")
		if lastObjEnd != -1 && lastObjEnd < len(s)-1 {
			return s[:lastObjEnd+1] + "]"
		}
		// Fallback to empty array if no object end found, but only if it looks like an array of objects.
		if lastObjEnd == -1 {
			if strings.Contains(s, "{") {
				return "[]"
			}
			return s
		}
	}
	return s
}

func repairStructural(s string) string {
	r := &structuralRepairer{
		runes: []rune(s),
	}
	return r.repair()
}

type structuralRepairer struct {
	runes    []rune
	pos      int
	stack    []rune
	sb       strings.Builder
	inString bool
	escaped  bool
}

func (r *structuralRepairer) repair() string {
	for r.pos < len(r.runes) {
		ch := r.runes[r.pos]

		if r.escaped {
			r.sb.WriteRune(ch)
			r.escaped = false
			r.pos++
			continue
		}

		if ch == '\\' {
			r.handleBackslash()
			continue
		}

		if ch == '"' {
			r.handleQuote()
			continue
		}

		if r.inString {
			r.handleStringContent(ch)
			continue
		}

		if result, done := r.handleStructure(ch); done {
			return result
		}
		r.pos++
	}

	r.closeOpenContexts()
	return r.sb.String()
}

func (r *structuralRepairer) handleBackslash() {
	if !r.inString {
		r.pos++
		return
	}

	if r.pos+1 < len(r.runes) && r.runes[r.pos+1] == '"' {
		if r.isFollowedByDelimiter(r.pos + 2) {
			r.sb.WriteRune('"')
			r.inString = false
			r.pos += 2
			return
		}
	}

	r.sb.WriteRune('\\')
	r.escaped = true
	r.pos++
}

func (r *structuralRepairer) isFollowedByDelimiter(startIdx int) bool {
	if startIdx >= len(r.runes) {
		return true
	}
	next := r.runes[startIdx]
	for k := startIdx; k < len(r.runes); k++ {
		c := r.runes[k]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			next = c
			break
		}
		if k == len(r.runes)-1 {
			return true
		}
	}
	return next == ',' || next == '}' || next == ']' || next == ':'
}

func (r *structuralRepairer) handleQuote() {
	if r.inString {
		if r.isFollowedByDelimiter(r.pos + 1) {
			r.inString = false
			r.sb.WriteRune('"')
		} else {
			r.sb.WriteString(`\"`)
		}
	} else {
		r.inString = true
		r.sb.WriteRune('"')
	}
	r.pos++
}

func (r *structuralRepairer) handleStringContent(ch rune) {
	closing := (ch == '}' && r.peek() == '{') || (ch == ']' && r.peek() == '[')

	if closing {
		r.inString = false
		r.sb.WriteRune('"')
		return
	}

	switch ch {
	case '\n':
		r.sb.WriteString("\\n")
	case '\r':
		r.sb.WriteString("\\r")
	case '\t':
		r.sb.WriteString("\\t")
	default:
		r.sb.WriteRune(ch)
	}
	r.pos++
}

func (r *structuralRepairer) handleStructure(ch rune) (string, bool) {
	switch ch {
	case '{', '[':
		r.stack = append(r.stack, ch)
		r.sb.WriteRune(ch)
	case '}':
		if r.peek() == '[' {
			r.sb.WriteRune(']')
			r.pop()
			if r.peek() == '{' {
				r.pop()
				r.sb.WriteRune('}')
			}
		} else if r.peek() == '{' {
			r.pop()
			r.sb.WriteRune('}')
		} else {
			r.sb.WriteRune('}')
		}
		if len(r.stack) == 0 {
			return r.sb.String(), true
		}
	case ']':
		if r.peek() == '{' {
			r.sb.WriteRune('}')
			r.pop()
			if r.peek() == '[' {
				r.pop()
			}
			r.sb.WriteRune(']')
		} else if r.peek() == '[' {
			r.pop()
			r.sb.WriteRune(']')
		} else {
			r.sb.WriteRune(']')
		}
		if len(r.stack) == 0 {
			return r.sb.String(), true
		}
	default:
		r.sb.WriteRune(ch)
	}
	return "", false
}

func (r *structuralRepairer) closeOpenContexts() {
	if r.inString {
		r.sb.WriteRune('"')
	}
	for i := len(r.stack) - 1; i >= 0; i-- {
		switch r.stack[i] {
		case '{':
			r.sb.WriteRune('}')
		case '[':
			r.sb.WriteRune(']')
		}
	}
}

func (r *structuralRepairer) peek() rune {
	if len(r.stack) == 0 {
		return 0
	}
	return r.stack[len(r.stack)-1]
}

func (r *structuralRepairer) pop() {
	if len(r.stack) > 0 {
		r.stack = r.stack[:len(r.stack)-1]
	}
}

func fixDanglingKey(s string) string {
	re := regexp.MustCompile(`,\s*"[^"]*"\s*}`)
	return re.ReplaceAllString(s, `}`)
}

func unmaskStrings(s string, originals []string) string {
	for i, orig := range originals {
		placeholder := fmt.Sprintf(`"__STR_%d__"`, i)
		s = strings.Replace(s, placeholder, orig, 1)
	}
	return s
}
