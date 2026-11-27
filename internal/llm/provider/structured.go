package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrInvalidStructuredOutput = errors.New("JSON不正")
	ErrResponseTooShort        = errors.New("response too short")
)

type StructuredSchema struct {
	Type       string                       `json:"type"`
	Properties map[string]*StructuredSchema `json:"properties,omitempty"`
	Items      *StructuredSchema            `json:"items,omitempty"`
	Required   []string                     `json:"required,omitempty"`
	Enum       []string                     `json:"enum,omitempty"`
}

func (s *StructuredSchema) Decode(content string, result any) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%w: 応答が空", ErrInvalidStructuredOutput)
	}
	data := []byte(content)
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return fmt.Errorf("%w: %dバイト目: %w", ErrInvalidStructuredOutput, syntaxErr.Offset, err)
		}
		return fmt.Errorf("%w: %w", ErrInvalidStructuredOutput, err)
	}
	if err := s.validateValue(value); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidStructuredOutput, err)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidStructuredOutput, err)
	}
	return nil
}

func (s *StructuredSchema) validateValue(value any) error {
	if s == nil {
		return nil
	}
	switch s.Type {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return errors.New("オブジェクトが必要")
		}
		for _, key := range s.Required {
			if _, exists := object[key]; !exists {
				return fmt.Errorf("必須項目 %s がない", key)
			}
		}
		for key, item := range object {
			if err := s.Properties[key].validateValue(item); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return errors.New("配列が必要")
		}
		for i, item := range items {
			if err := s.Items.validateValue(item); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return errors.New("文字列が必要")
		}
		if len(s.Enum) > 0 && !slices.Contains(s.Enum, text) {
			return errors.New("許可されていない値")
		}
	}
	return nil
}
