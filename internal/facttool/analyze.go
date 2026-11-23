package facttool

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

type reportFact struct {
	Target    string `json:"target"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
	Timestamp string `json:"timestamp"`
}

type targetKey struct {
	target string
	key    string
}

type duplicateGroup struct {
	targetKey
	count int
}

func Analyze(path string, out io.Writer) error {
	facts, err := ReadFile[reportFact](path)
	if err != nil {
		return err
	}

	type factValue struct {
		targetKey
		value string
	}
	uniqueFacts := make(map[factValue]struct{})
	counts := make(map[targetKey]int)
	for _, fact := range facts {
		pair := targetKey{target: fact.Target, key: fact.Key}
		value := strings.TrimSpace(fmt.Sprint(fact.Value))
		uniqueFacts[factValue{targetKey: pair, value: value}] = struct{}{}
		counts[pair]++
	}

	fmt.Fprintf(out, "Total Facts: %d\n", len(facts))
	fmt.Fprintf(out, "Unique Facts (Target+Key+Value): %d\n", len(uniqueFacts))
	fmt.Fprintf(out, "Duplication Rate: %.2f%%\n", (1-float64(len(uniqueFacts))/float64(len(facts)))*100)
	fmt.Fprintf(out, "Unique Target+Key pairs: %d\n", len(counts))
	fmt.Fprintln(out, "\n=== Top Keys with Multiple Values (Target | Key) ===")

	var groups []duplicateGroup
	for pair, count := range counts {
		if count > 1 {
			groups = append(groups, duplicateGroup{targetKey: pair, count: count})
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].count > groups[j].count
	})
	for i, group := range groups[:min(20, len(groups))] {
		fmt.Fprintf(out, "%d: %s (Key: %s) - %d entries\n", i+1, group.target, group.key, group.count)
	}

	if len(groups) > 0 {
		top := groups[0]
		fmt.Fprintf(out, "\n--- Values for top duplicate [%s | %s] ---\n", top.target, top.key)
		shown := 0
		for _, fact := range facts {
			if fact.Target == top.target && fact.Key == top.key {
				fmt.Fprintf(out, "- %v (Timestamp: %v)\n", fact.Value, fact.Timestamp)
				shown++
				if shown == 5 {
					break
				}
			}
		}
	}
	return nil
}
