package llm

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// ErrorNotifierFunc defines the callback signature for error notifications
type ErrorNotifierFunc func(message, details string)

var errorNotifier ErrorNotifierFunc

// Pre-compiled regexes for performance optimization
var (
	reMissingCommaQuotes     = regexp.MustCompile(`:"([a-zA-Z0-9_]+),"([a-zA-Z0-9_]+)":`)
	reMergedKeyValue         = regexp.MustCompile(`([,{]\s*)"(value)([^":,]+)"`)
	reHexEscapes             = regexp.MustCompile(`\\x([0-9a-fA-F]{2})`)
	reStringLiterals         = regexp.MustCompile(`"[^"\\]*(?:\\.[^"\\]*)*"`)
	reUnquotedKeys           = regexp.MustCompile(`([,{]\s*)([a-zA-Z0-9_]+)\s*:`)
	reJapaneseOpeningQuote   = regexp.MustCompile(`:\s*「`)
	reJapaneseClosingQuote   = regexp.MustCompile(`」(\s*[\}\],])`)
	reMissingCommaValueKey   = regexp.MustCompile(`("[^"]*")(\s*)("[a-zA-Z0-9_]+"\s*:)`)
	reMissingCommaAfterValue = regexp.MustCompile(`("[^"]*"|true|false|null|[0-9]+)\s*([{\[])`)
	reUnexpectedColon        = regexp.MustCompile(`(:\s*"(\\.|[^"\\])*")\s*:\s*`)
	reInvalidKeyFormat       = regexp.MustCompile(`"?([a-zA-Z0-9_]+)"?\s*=\s*`)
	reSemicolonSeparator     = regexp.MustCompile(`([}\]])\s*;\s*([{\[])`)
	reMissingCommaObjects    = regexp.MustCompile(`([}\]])\s+([{\[])`)
	reUnquotedValues         = regexp.MustCompile(`([\[,])\s*([^"\[\{\]\}\s,][^"\[\{\]\}\s,]*?)("?)\s*([,\]])`)
	reGarbageQuotes          = regexp.MustCompile(`([^:,\s\[\{])""(\s*[\}\],])`)
	reMissingOpeningQuotes   = regexp.MustCompile(`(:\s*)([^"\[\{\]\}\s\\][^,}\]]*)`)
	reGarbageKeyAfterObject  = regexp.MustCompile(`}\s*([a-zA-Z0-9_]+)\s*:`)
	reInvalidObjectToArray   = regexp.MustCompile(`\{\s*"[^"]+"(?:\s*,\s*"[^"]+")*\s*\}`)
	reTrailingCommas         = regexp.MustCompile(`(["}\]el0-9])\s*,\s*([}\]])`)
	reDanglingKey            = regexp.MustCompile(`,\s*"[^"]*"\s*}`)
	reBareKeyValue           = regexp.MustCompile(`([\[,]\s*)("[^"]+"\s*:\s*"[^"]+")`)
	reFullWidthColonQuoted   = regexp.MustCompile(`"\s*\x{ff1a}`)
	reFullWidthColonKey      = regexp.MustCompile(`([{\[,]\s*"[^"]*?)\s*\x{ff1a}`)
	rePlaceholder            = regexp.MustCompile(`"?__STR_(\d+)__"?`)
	reMissingClosingBrace    = regexp.MustCompile(`(:\s*(?:"[^"]*"|true|false|null|[0-9\.-]+)\s*),\s*\{`)
)

// SetErrorNotifier sets the callback function for error notifications.
func SetErrorNotifier(notifier ErrorNotifierFunc) {
	errorNotifier = notifier
}

// UnmarshalWithRepair attempts unmarshal; retries with repair on failure.
// Logs detailed error only if repair also fails.
func UnmarshalWithRepair(jsonStr string, v interface{}, logPrefix string) error {
	// Phase 1: Standard Unmarshal
	if err := json.Unmarshal([]byte(jsonStr), v); err == nil {
		return nil
	}

	// Phase 2: Tier 1 - Structural Repair
	t1 := repairTier1(jsonStr)
	if err := json.Unmarshal([]byte(t1), v); err == nil {
		return nil
	}

	// Phase 3: Tier 2 - Character Repair
	t2 := repairTier2(jsonStr)
	if err := json.Unmarshal([]byte(t2), v); err == nil {
		return nil
	}

	// Phase 4: Tier 3 - Quote Repair
	t3 := repairTier3(jsonStr)
	if err := json.Unmarshal([]byte(t3), v); err == nil {
		return nil
	}

	// Phase 5: Tier 4 - Aggressive Repair
	t4 := repairTier4(jsonStr)
	if err := json.Unmarshal([]byte(t4), v); err == nil {
		// Handle specific array-of-objects wrapped in [] case if needed, though RepairJSON covers some
		return nil
	}

	// Special case: Try wrapping in array if it looks like a slice of objects
	if typeErr, ok := json.Unmarshal([]byte(t4), &v).(*json.UnmarshalTypeError); ok {
		if typeErr.Type.Kind() == reflect.Slice && typeErr.Value == "object" {
			arrayWrapped := "[" + t4 + "]"
			if err := json.Unmarshal([]byte(arrayWrapped), v); err == nil {
				return nil
			}
		}
	}

	err := fmt.Errorf("failed to parse JSON after 4-tier repair")
	msg := fmt.Sprintf("%sJSONパースエラー(修復後): %v", logPrefix, err)
	detail := fmt.Sprintf("Original: %s\nLastRepaired: %s", jsonStr, t4)
	log.Printf("%s\n%s", msg, detail)

	if errorNotifier != nil {
		go errorNotifier(msg, detail)
	}
	return err
}

// repairTier1 applies minimal structural repairs.
func repairTier1(s string) string {
	s = fixTrailingCommas(s)
	s = repairDoubleArray(s)
	s = repairTruncatedArray(s)
	s = repairStructural(s)
	s = fixInvalidObjectToArray(s)
	s = fixMissingCommaBetweenObjects(s)
	s = repairDoubleArray(s)
	return strings.TrimSpace(s)
}

// repairTier2 applies character level fixes
func repairTier2(s string) string {
	s = fixEscapedSingleQuotes(s)
	s = fixHexEscapes(s)
	s = fixSemicolonSeparator(s)
	s = fixMergedKeyValue(s)
	s = fixJapaneseOpeningQuote(s)
	s = fixJapaneseClosingQuote(s)
	return repairTier1(s)
}

// repairTier3 applies quote and key fixes.
func repairTier3(s string) string {
	s = fixEscapedSingleQuotes(s)
	s = fixHexEscapes(s)
	s = fixSemicolonSeparator(s)
	s = fixMergedKeyValue(s)
	s = fixUnquotedKeys(s)
	s = fixMissingCommaQuotes(s)
	s = fixMissingCommaBetweenValueAndKey(s)
	s = fixMissingCommaAfterValue(s)
	s = fixUnexpectedColon(s)
	s = fixInvalidKeyFormat(s)
	s = fixGarbageQuotes(s)
	s = fixGarbageKeyAfterObject(s)
	s = fixMissingOpeningQuotes(s)
	s = fixUnquotedValuesInArray(s)
	s = fixDanglingKey(s)

	s = fixJapaneseOpeningQuote(s)
	s = fixJapaneseClosingQuote(s)
	return repairTier1(s)
}

// repairTier4 attempts to repair a truncated or malformed JSON string.
// repairs unclosed arrays and stack-based structural issues.
func repairTier4(s string) string {
	s = fixFullWidthColons(s)
	s = fixEscapedSingleQuotes(s)
	s = fixMissingCommaQuotes(s)
	s = fixMergedKeyValue(s)
	s = fixHexEscapes(s)
	s = fixUnquotedValuesInArray(s)

	s, originals := maskStrings(s)

	s = applyComplexRegexRepairs(s)
	s = strings.TrimSpace(s)

	s = repairDoubleArray(s)
	s = repairTruncatedArray(s)
	s = repairStructural(s)
	s = fixDanglingKey(s)

	s = unmaskStrings(s, originals)
	return s
}

// applyComplexRegexRepairs applies aggressive regex-based fixes on a MASKED string.
// Formerly known as preprocessJSON.
func applyComplexRegexRepairs(s string) string {
	s = fixDoubleCommas(s)

	s = fixUnquotedKeys(s)
	s = fixJapaneseOpeningQuote(s)
	s = fixJapaneseClosingQuote(s)
	s = fixMissingCommaBetweenValueAndKey(s)
	s = fixMissingCommaAfterValue(s)
	s = fixUnexpectedColon(s)
	s = fixInvalidKeyFormat(s)
	s = fixSemicolonSeparator(s)
	s = fixMissingCommaBetweenObjects(s)
	s = fixGarbageQuotes(s)
	s = fixMissingOpeningQuotes(s)
	s = fixGarbageKeyAfterObject(s)
	s = fixInvalidObjectToArray(s)
	s = fixTrailingCommas(s)
	s = fixBareKeyValueInArray(s)
	s = fixMissingClosingBrace(s)

	return s
}

// --- Tier 1 Helpers ---

func fixTrailingCommas(s string) string {
	return reTrailingCommas.ReplaceAllString(s, `$1$2`)
}

func repairDoubleArray(s string) string {
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		return s[1 : len(s)-1]
	}
	return s
}

func repairTruncatedArray(s string) string {
	if strings.HasPrefix(s, "[") && !strings.HasSuffix(s, "]") {
		lastObjEnd := strings.LastIndex(s, "}")
		if lastObjEnd != -1 && lastObjEnd < len(s)-1 {
			return s[:lastObjEnd+1] + "]"
		}
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
	done     bool
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

		r.handleStructure(ch)
		if r.done {
			return r.sb.String()
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
		if r.pos+1 < len(r.runes) && r.runes[r.pos+1] == '"' && r.isFollowedByDelimiter(r.pos+2) {
			r.inString = false
			r.sb.WriteRune('"')
			r.pos += 2
			return
		}
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

func (r *structuralRepairer) handleStructure(ch rune) {
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
			r.done = true
			return
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
			r.done = true
			return
		}
	default:
		r.sb.WriteRune(ch)
	}
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

func fixInvalidObjectToArray(s string) string {
	return reInvalidObjectToArray.ReplaceAllStringFunc(s, func(match string) string {
		return "[" + match[1:len(match)-1] + "]"
	})
}

func fixMissingCommaBetweenObjects(s string) string {
	return reMissingCommaObjects.ReplaceAllString(s, `$1,$2`)
}

// --- Tier 2 Helpers ---

func fixFullWidthColons(s string) string {
	s = reFullWidthColonKey.ReplaceAllString(s, `$1": `)
	return reFullWidthColonQuoted.ReplaceAllString(s, `": `)
}

func fixEscapedSingleQuotes(s string) string {
	return strings.ReplaceAll(s, `\'`, `'`)
}

func fixHexEscapes(s string) string {
	return reHexEscapes.ReplaceAllStringFunc(s, func(match string) string {
		return "\\u00" + match[2:]
	})
}

func fixSemicolonSeparator(s string) string {
	return reSemicolonSeparator.ReplaceAllString(s, `$1,$2`)
}

func fixMergedKeyValue(s string) string {
	return reMergedKeyValue.ReplaceAllStringFunc(s, func(match string) string {
		if strings.Contains(match, "：") {
			return match
		}
		return reMergedKeyValue.ReplaceAllString(match, `$1"$2":"$3"`)
	})
}

func fixJapaneseOpeningQuote(s string) string {
	return reJapaneseOpeningQuote.ReplaceAllString(s, `: "`)
}

func fixJapaneseClosingQuote(s string) string {
	return reJapaneseClosingQuote.ReplaceAllString(s, `"$1`)
}

// --- Tier 3 Helpers ---

func fixUnquotedKeys(s string) string {
	return reUnquotedKeys.ReplaceAllString(s, `$1"$2":`)
}

func fixMissingCommaQuotes(s string) string {
	return reMissingCommaQuotes.ReplaceAllString(s, `:"$1","$2":`)
}

func fixMissingCommaBetweenValueAndKey(s string) string {
	return reMissingCommaValueKey.ReplaceAllString(s, `$1,$2$3`)
}

func fixMissingCommaAfterValue(s string) string {
	return reMissingCommaAfterValue.ReplaceAllString(s, `$1, $2`)
}

func fixUnexpectedColon(s string) string {
	return reUnexpectedColon.ReplaceAllString(s, `$1, "repaired_key":`)
}

func fixInvalidKeyFormat(s string) string {
	return reInvalidKeyFormat.ReplaceAllString(s, `"$1":`)
}

func fixGarbageQuotes(s string) string {
	return reGarbageQuotes.ReplaceAllString(s, `$1"$2`)
}

func fixGarbageKeyAfterObject(s string) string {
	return reGarbageKeyAfterObject.ReplaceAllString(s, `, "$1":`)
}

func fixMissingOpeningQuotes(s string) string {
	return reMissingOpeningQuotes.ReplaceAllStringFunc(s, func(match string) string {
		groups := reMissingOpeningQuotes.FindStringSubmatch(match)
		if len(groups) < 3 {
			return match
		}
		prefix := groups[1]
		val := strings.TrimSpace(groups[2])

		if val == "true" || val == "false" || val == "null" {
			return match
		}
		isNumber := true
		for _, r := range val {
			if (r < '0' || r > '9') && r != '.' && r != '-' {
				isNumber = false
				break
			}
		}
		if isNumber && len(val) > 0 {
			return match
		}

		return fmt.Sprintf(`%s"%s"`, prefix, val)
	})
}

func fixUnquotedValuesInArray(s string) string {
	for {
		newS := reUnquotedValues.ReplaceAllStringFunc(s, func(match string) string {
			groups := reUnquotedValues.FindStringSubmatch(match)
			if len(groups) < 5 {
				return match
			}
			prefix := groups[1]
			val := strings.TrimSpace(groups[2])
			suffix := groups[4]

			if val == "true" || val == "false" || val == "null" {
				return match
			}
			if _, err := fmt.Sscanf(val, "%f", new(float64)); err == nil {
				isNumber := true
				for _, r := range val {
					if (r < '0' || r > '9') && r != '.' && r != '-' && r != 'e' && r != 'E' && r != '+' {
						isNumber = false
						break
					}
				}
				if isNumber {
					return match
				}
			}

			return fmt.Sprintf(`%s"%s"%s`, prefix, val, suffix)
		})
		if newS == s {
			break
		}
		s = newS
	}
	return s
}

func fixDanglingKey(s string) string {
	return reDanglingKey.ReplaceAllString(s, `}`)
}

// --- Tier 4 Helpers ---

func maskStrings(s string) (string, []string) {
	var originals []string
	masked := reStringLiterals.ReplaceAllStringFunc(s, func(match string) string {
		placeholder := fmt.Sprintf(`"__STR_%d__"`, len(originals))

		match = strings.ReplaceAll(match, "\n", "\\n")
		match = strings.ReplaceAll(match, "\r", "\\r")
		match = strings.ReplaceAll(match, "\t", "\\t")

		originals = append(originals, match)
		return placeholder
	})
	return masked, originals
}

func unmaskStrings(s string, originals []string) string {
	return rePlaceholder.ReplaceAllStringFunc(s, func(match string) string {
		nums := regexp.MustCompile(`\d+`).FindString(match)
		if idx, err := strconv.Atoi(nums); err == nil {
			if idx >= 0 && idx < len(originals) {
				return originals[idx]
			}
		}
		return match
	})
}

func fixDoubleCommas(s string) string {
	return regexp.MustCompile(`,,+`).ReplaceAllString(s, ",")
}

func fixBareKeyValueInArray(s string) string {
	matches := reBareKeyValue.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}

	var sb strings.Builder
	lastPos := 0
	braceDepth := 0
	matchIdx := 0

	for i := 0; i < len(s); i++ {
		if matchIdx < len(matches) && i == matches[matchIdx][0] {
			if braceDepth == 0 {
				m := matches[matchIdx]
				sb.WriteString(s[lastPos:i])
				sb.WriteString(s[m[2]:m[3]])
				sb.WriteString("{")
				sb.WriteString(s[m[4]:m[5]])
				sb.WriteString("}")
				lastPos = m[1]
				i = m[1] - 1
			}
			matchIdx++
			continue
		}

		switch s[i] {
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		}
	}
	sb.WriteString(s[lastPos:])
	return sb.String()
}

func fixMissingClosingBrace(s string) string {
	return reMissingClosingBrace.ReplaceAllString(s, `$1}, {`)
}
