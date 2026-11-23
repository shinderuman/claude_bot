package store

import (
	"strings"

	"claude_bot/internal/model"
)

func filterFuzzyFacts(facts []model.Fact, targets, keys []string) []model.Fact {
	var results []model.Fact
	for _, fact := range facts {
		if matchesFactTarget(fact, targets) && matchesFactKey(fact, keys) {
			results = append(results, fact)
		}
	}
	return results
}

func matchesFactTarget(fact model.Fact, targets []string) bool {
	for _, target := range targets {
		if fact.Target == target || fact.TargetUserName == target {
			return true
		}
		if len(target) >= MinTargetUserNameFuzzyLength &&
			(strings.HasPrefix(fact.TargetUserName, target) || strings.HasSuffix(fact.TargetUserName, target)) {
			return true
		}
	}
	return len(targets) == 0
}

func matchesFactKey(fact model.Fact, keys []string) bool {
	for _, key := range keys {
		if strings.Contains(fact.Key, key) || strings.Contains(key, fact.Key) {
			return true
		}
		if strings.HasPrefix(fact.Key, model.SystemFactKeyPrefix) && strings.Contains(fact.Value, key) {
			return true
		}
	}
	return len(keys) == 0
}
