package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"claude_bot/internal/util"

	"github.com/gofrs/flock"
	"github.com/joho/godotenv"
)

const (
	RegistryFileName = "cluster_registry.json"
	// Keep the registry TTL longer than the maintenance interval.
	HeartbeatDuration = 7 * time.Hour
	HeartbeatInterval = 5 * time.Minute
)

type Registry struct {
	Nodes []Node `json:"nodes"`
}

type Node struct {
	Username    string    `json:"username"`
	LastUpdated time.Time `json:"last_updated"`
	JoinedAt    time.Time `json:"joined_at"`
}

func StartHeartbeatLoop(ctx context.Context, username string) {
	if _, _, err := GetMyPosition(username); err != nil {
		log.Printf("[Discovery] Initial registration failed: %v", err)
	}

	ticker := time.NewTicker(HeartbeatInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, _, err := GetMyPosition(username); err != nil {
					log.Printf("[Discovery] Heartbeat failed: %v", err)
				}
			}
		}
	}()
}

func GetMyPosition(username string) (int, int, error) {
	registryPath := util.GetFilePath(RegistryFileName)
	lockPath := registryPath + ".lock"

	fileLock := flock.New(lockPath)

	locked, err := fileLock.TryLockContext(context.TODO(), 1000*time.Millisecond)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create/acquire lock: %w", err)
	}
	if !locked {
		log.Println("[Discovery] Lock busy, retrying...")
		time.Sleep(500 * time.Millisecond)
		locked, err = fileLock.TryLockContext(context.TODO(), 1000*time.Millisecond)
		if err != nil || !locked {
			return 0, 0, fmt.Errorf("could not acquire lock on registry")
		}
	}
	defer fileLock.Unlock() //nolint:errcheck

	if err := os.MkdirAll(filepath.Dir(registryPath), 0755); err != nil {
		return 0, 0, err
	}

	var registry Registry
	data, err := os.ReadFile(registryPath)
	if err == nil {
		if len(data) > 0 {
			if err := json.Unmarshal(data, &registry); err != nil {
				log.Printf("[Discovery] Warning: Failed to unmarshal registry (will overwrite): %v", err)
			}
		} else {
			log.Println("[Discovery] Registry file is empty.")
		}
	} else {
		log.Printf("[Discovery] Failed to read registry (new?): %v", err)
	}

	now := time.Now()
	threshold := now.Add(-HeartbeatDuration)

	nodeMap := make(map[string]Node)

	for _, node := range registry.Nodes {
		if node.Username == username {
			continue
		}

		if !node.LastUpdated.After(threshold) {
			log.Printf("[Discovery] Dropping expired node: %s (Last seen: %s)", node.Username, node.LastUpdated)
			continue
		}

		if existing, exists := nodeMap[node.Username]; exists {
			bestJoinedAt := existing.JoinedAt
			if bestJoinedAt.IsZero() || (!node.JoinedAt.IsZero() && node.JoinedAt.Before(bestJoinedAt)) {
				bestJoinedAt = node.JoinedAt
			}

			bestLastUpdated := existing.LastUpdated
			if node.LastUpdated.After(bestLastUpdated) {
				bestLastUpdated = node.LastUpdated
			}

			merged := Node{
				Username:    node.Username,
				LastUpdated: bestLastUpdated,
				JoinedAt:    bestJoinedAt,
			}
			nodeMap[node.Username] = merged
		} else {
			nodeMap[node.Username] = node
		}
	}

	var selfNode Node
	var oldSelf *Node
	for _, node := range registry.Nodes {
		if node.Username == username {
			oldSelf = &node
			break
		}
	}

	if oldSelf != nil {
		selfNode = *oldSelf
		selfNode.LastUpdated = now
		if selfNode.JoinedAt.IsZero() {
			selfNode.JoinedAt = now
		}
	} else {
		selfNode = Node{
			Username:    username,
			LastUpdated: now,
			JoinedAt:    now,
		}
	}
	nodeMap[username] = selfNode

	var activeNodes []Node
	for _, node := range nodeMap {
		activeNodes = append(activeNodes, node)
	}

	sort.Slice(activeNodes, func(i, j int) bool {
		if !activeNodes[i].JoinedAt.Equal(activeNodes[j].JoinedAt) {
			return activeNodes[i].JoinedAt.Before(activeNodes[j].JoinedAt)
		}
		return activeNodes[i].Username < activeNodes[j].Username
	})

	registry.Nodes = activeNodes
	newData, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return 0, 0, fmt.Errorf("failed to marshal registry: %w", err)
	}

	if err := os.WriteFile(registryPath, newData, 0644); err != nil {
		return 0, 0, fmt.Errorf("failed to write registry: %w", err)
	}

	myIndex := -1
	for i, node := range activeNodes {
		if node.Username == username {
			myIndex = i
			break
		}
	}

	if myIndex == -1 {
		return 0, 1, nil
	}

	return myIndex, len(activeNodes), nil
}

func GetKnownBotUsernames(dataDir string) ([]string, error) {

	files, err := os.ReadDir(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read data directory: %w", err)
	}

	uniqueNames := make(map[string]bool)
	var usernames []string

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		if len(name) < 4 || name[:4] != ".env" || name == ".env.example" {
			continue
		}

		path := filepath.Join(dataDir, name)
		envMap, err := godotenv.Read(path)
		if err != nil {
			log.Printf("Warning: failed to read env file %s: %v", name, err)
			continue
		}

		username := envMap["BOT_USERNAME"]
		if username == "" {
			continue
		}

		if uniqueNames[username] {
			continue
		}

		uniqueNames[username] = true
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)
	return usernames, nil
}

func GetKnownBotUsernamesMap(dataDir string) (map[string]struct{}, error) {
	names, err := GetKnownBotUsernames(dataDir)
	if err != nil {
		return nil, err
	}

	botMap := make(map[string]struct{}, len(names))
	for _, name := range names {
		botMap[name] = struct{}{}
	}
	return botMap, nil
}
