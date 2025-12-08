package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

type Fact struct {
	Target    string      `json:"target"`
	Key       string      `json:"key"`
	Value     interface{} `json:"value"`
	Timestamp string      `json:"timestamp"` // Use string for easier unmarshal in script
}

type kv struct {
	Key   string
	Count int
}

func main() {
	data, err := os.ReadFile("data/facts.json")
	if err != nil {
		log.Fatalf("Failed to read facts.json: %v", err)
	}

	var facts []Fact
	if err := json.Unmarshal(data, &facts); err != nil {
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	total := len(facts)
	uniqueMap := make(map[string]int)
	targetKeyMap := make(map[string]int)

	for _, f := range facts {
		valStr := fmt.Sprintf("%v", f.Value)
		// Normalize for comparison
		valStr = strings.TrimSpace(valStr)

		uniqueKey := fmt.Sprintf("%s|%s|%s", f.Target, f.Key, valStr)
		uniqueMap[uniqueKey]++

		tkKey := fmt.Sprintf("%s|%s", f.Target, f.Key)
		targetKeyMap[tkKey]++
	}

	fmt.Printf("Total Facts: %d\n", total)
	fmt.Printf("Unique Facts (Target+Key+Value): %d\n", len(uniqueMap))
	fmt.Printf("Duplication Rate: %.2f%%\n", (1.0-float64(len(uniqueMap))/float64(total))*100)

	fmt.Printf("Unique Target+Key pairs: %d\n", len(targetKeyMap))

	fmt.Println("\n=== Top Keys with Multiple Values (Target | Key) ===")
	var tkSorted []kv
	for k, v := range targetKeyMap {
		if v > 1 {
			tkSorted = append(tkSorted, kv{k, v})
		}
	}
	sort.Slice(tkSorted, func(i, j int) bool {
		return tkSorted[i].Count > tkSorted[j].Count
	})

	for i, item := range tkSorted {
		if i >= 20 {
			break
		}
		parts := strings.Split(item.Key, "|")
		fmt.Printf("%d: %s (Key: %s) - %d entries\n", i+1, parts[0], parts[1], item.Count)
	}

	// Show sample values for the top duplicate
	if len(tkSorted) > 0 {
		topKey := tkSorted[0].Key
		parts := strings.Split(topKey, "|")
		target, key := parts[0], parts[1]
		fmt.Printf("\n--- Values for top duplicate [%s | %s] ---\n", target, key)

		count := 0
		for _, f := range facts {
			if f.Target == target && f.Key == key {
				fmt.Printf("- %v (Timestamp: %v)\n", f.Value, f.Timestamp) // Accessing Timestamp might require struct update but it's not in struct definition above. Let's add it.
				count++
				if count >= 5 {
					break
				}
			}
		}
	}
}
