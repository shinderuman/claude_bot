package util

import (
	"encoding/json"
	"fmt"
	"os"
)

func ReadJSONArray[T any](path string) ([]T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var rows []T
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("parse JSON in %s: %w", path, err)
	}
	if rows == nil {
		return nil, fmt.Errorf("expected a JSON array in %s", path)
	}
	return rows, nil
}
