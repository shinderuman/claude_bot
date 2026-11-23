package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"
	"time"

	"claude_bot/internal/util"

	"github.com/gofrs/flock"
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

	ticker := time.NewTicker(HeartbeatInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, _, err := GetMyPosition(ctx, username); err != nil {
					log.Printf("クラスタのハートビート更新失敗: %v", err)
				}
			}
		}
	}()
}

func GetMyPosition(ctx context.Context, username string) (int, int, error) {
	registryPath, err := util.DataFilePath(RegistryFileName)
	if err != nil {
		return 0, 0, err
	}
	return registerNode(ctx, registryPath, username)
}

func registerNode(ctx context.Context, path, username string) (int, int, error) {
	fileLock := flock.New(path + ".lock")
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	locked, err := fileLock.TryLockContext(lockCtx, 100*time.Millisecond)
	if err != nil {
		return 0, 0, fmt.Errorf("could not acquire lock on registry: %w", err)
	}
	if !locked {
		return 0, 0, fmt.Errorf("could not acquire lock on registry")
	}
	defer fileLock.Unlock() //nolint:errcheck

	registry, err := readRegistry(path)
	if err != nil {
		return 0, 0, err
	}

	nodes := refreshNodes(registry.Nodes, username, time.Now())
	data, err := json.MarshalIndent(Registry{Nodes: nodes}, "", "  ")
	if err != nil {
		return 0, 0, fmt.Errorf("failed to marshal registry: %w", err)
	}
	if err := util.WriteFileAtomically(path, data); err != nil {
		return 0, 0, fmt.Errorf("failed to write registry: %w", err)
	}
	index := slices.IndexFunc(nodes, func(node Node) bool { return node.Username == username })
	return index, len(nodes), nil
}

func readRegistry(path string) (Registry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Registry{}, nil
	}
	if err != nil {
		return Registry{}, fmt.Errorf("failed to read registry: %w", err)
	}
	if len(data) == 0 {
		return Registry{}, nil
	}

	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return Registry{}, fmt.Errorf("failed to parse registry: %w", err)
	}
	return registry, nil
}

func refreshNodes(previous []Node, username string, now time.Time) []Node {
	threshold := now.Add(-HeartbeatDuration)
	byName := make(map[string]Node, len(previous)+1)
	for _, node := range previous {
		if node.Username != username && !node.LastUpdated.After(threshold) {
			log.Printf("期限切れBotをクラスタから除外: %s、最終更新 %s", node.Username, node.LastUpdated)
			continue
		}
		existing, exists := byName[node.Username]
		if exists {
			if existing.LastUpdated.After(node.LastUpdated) {
				node.LastUpdated = existing.LastUpdated
			}
			if !existing.JoinedAt.IsZero() && (node.JoinedAt.IsZero() || existing.JoinedAt.Before(node.JoinedAt)) {
				node.JoinedAt = existing.JoinedAt
			}
		}
		byName[node.Username] = node
	}
	self := byName[username]
	self.Username = username
	self.LastUpdated = now
	if self.JoinedAt.IsZero() {
		self.JoinedAt = now
	}
	byName[username] = self

	active := make([]Node, 0, len(byName))
	for _, node := range byName {
		active = append(active, node)
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].JoinedAt.Equal(active[j].JoinedAt) {
			return active[i].Username < active[j].Username
		}
		return active[i].JoinedAt.Before(active[j].JoinedAt)
	})
	return active
}
