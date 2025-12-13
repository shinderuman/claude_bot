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

	"claude_bot/internal/utils"

	"github.com/gofrs/flock"
)

const (
	// RegistryFile is the filename of the cluster registry file
	RegistryFileName = "cluster_registry.json"
	// HeartbeatDuration determines how long a registration is valid
	// Should be slightly longer than the maintenance interval
	HeartbeatDuration = 7 * time.Hour
	// HeartbeatInterval determines how often we update our registration
	HeartbeatInterval = 5 * time.Minute
)

type Registry struct {
	Nodes []Node `json:"nodes"`
}

type Node struct {
	Username    string    `json:"username"`
	LastUpdated time.Time `json:"last_updated"`
}

// StartHeartbeatLoop starts a background loop to keep the bot registered
func StartHeartbeatLoop(ctx context.Context, username string) {
	// 初回登録
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

// GetMyPosition registers the current bot and returns its position and total count
func GetMyPosition(username string) (int, int, error) {
	// utils.GetFilePath already handles the "data/" prefix logic
	registryPath := utils.GetFilePath(RegistryFileName)
	lockPath := registryPath + ".lock"

	// Create/Acquire Lock
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

	// MkdirAllは絶対パスでもOK
	if err := os.MkdirAll(filepath.Dir(registryPath), 0755); err != nil {
		return 0, 0, err
	}

	var registry Registry
	data, err := os.ReadFile(registryPath)
	if err == nil {
		if len(data) > 0 {
			if err := json.Unmarshal(data, &registry); err != nil {
				log.Printf("[Discovery] Warning: Failed to unmarshal registry (will overwrite): %v", err)
				// 読み込み失敗時は空として扱う（追記ではなく新規作成になる）
			} else {

			}
		} else {
			log.Println("[Discovery] Registry file is empty.")
		}
	} else {
		log.Printf("[Discovery] Failed to read registry (new?): %v", err)
	}

	// Update list: Remove expired nodes, Update/Add self
	now := time.Now()
	threshold := now.Add(-HeartbeatDuration)
	var activeNodes []Node
	foundSelf := false

	for _, node := range registry.Nodes {
		if node.Username == username {
			// Update self
			activeNodes = append(activeNodes, Node{Username: username, LastUpdated: now})
			foundSelf = true
		} else if node.LastUpdated.After(threshold) {
			// Keep active node
			activeNodes = append(activeNodes, node)
		} else {
			log.Printf("[Discovery] Dropping expired node: %s (Last seen: %s)", node.Username, node.LastUpdated)
		}
	}

	if !foundSelf {
		log.Printf("[Discovery] Adding new node: %s", username)
		activeNodes = append(activeNodes, Node{Username: username, LastUpdated: now})
	}

	// Sort nodes by username to ensure deterministic ordering
	sort.Slice(activeNodes, func(i, j int) bool {
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
