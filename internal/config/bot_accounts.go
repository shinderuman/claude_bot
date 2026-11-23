package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

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
		if !strings.HasPrefix(name, ".env") || name == ".env.example" {
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
