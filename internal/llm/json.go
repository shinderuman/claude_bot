package llm

import "strings"

func ExtractJSON(s string) string {
	s = strings.ReplaceAll(s, "```json", "")
	s = strings.ReplaceAll(s, "```", "")

	startObj := strings.Index(s, "{")
	startArr := strings.Index(s, "[")

	start := -1
	if startObj != -1 && startArr != -1 {
		start = min(startObj, startArr)
	} else if startObj != -1 {
		start = startObj
	} else if startArr != -1 {
		start = startArr
	}

	if start == -1 {
		return "{}"
	}

	endObj := strings.LastIndex(s, "}")
	endArr := strings.LastIndex(s, "]")

	end := -1
	if endObj != -1 && endArr != -1 {
		end = max(endObj, endArr)
	} else if endObj != -1 {
		end = endObj
	} else if endArr != -1 {
		end = endArr
	}

	if end == -1 || start > end {
		return "{}"
	}

	return s[start : end+1]
}
