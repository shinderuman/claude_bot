package facttool

import (
	"encoding/json"
	"fmt"
	"os"
)

func ReadFile[T any](path string) ([]T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read facts file: %w", err)
	}

	var facts []T
	if err := json.Unmarshal(data, &facts); err != nil {
		return nil, fmt.Errorf("Failed to parse facts JSON: %w", err)
	}
	return facts, nil
}
