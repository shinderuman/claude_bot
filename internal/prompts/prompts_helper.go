package prompts

const MinFactsRetentionRatio = 0.05

func truncateFactsByPriority(facts string, priority float64, includeCharacterPrompt bool) string {
	if !includeCharacterPrompt {
		return facts
	}

	allowedRatio := calculateAllowedRatio(priority)

	runes := []rune(facts)
	if len(runes) == 0 {
		return ""
	}

	allowedLen := int(float64(len(runes)) * allowedRatio)
	if allowedLen < len(runes) {
		return string(runes[:allowedLen]) + "\n... (truncated)"
	}

	return facts
}

func calculateAllowedRatio(priority float64) float64 {
	ratio := 1.0 - priority
	if ratio < MinFactsRetentionRatio {
		return MinFactsRetentionRatio
	}
	return ratio
}
