package provider

import (
	"encoding/json"
	"errors"
	"slices"
)

var (
	ErrInvalidStructuredOutput = errors.New("invalid structured output")
	ErrResponseTooShort        = errors.New("response too short")
)

type StructuredSchema struct {
	Type       string                       `json:"type"`
	Properties map[string]*StructuredSchema `json:"properties,omitempty"`
	Items      *StructuredSchema            `json:"items,omitempty"`
	Required   []string                     `json:"required,omitempty"`
	Enum       []string                     `json:"enum,omitempty"`
}

func (s *StructuredSchema) Accepts(content string) bool {
	var value any
	return json.Unmarshal([]byte(content), &value) == nil && s.acceptsValue(value)
}

func (s *StructuredSchema) acceptsValue(value any) bool {
	if s == nil {
		return true
	}
	switch s.Type {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for _, key := range s.Required {
			if _, exists := object[key]; !exists {
				return false
			}
		}
		for key, item := range object {
			if !s.Properties[key].acceptsValue(item) {
				return false
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if !s.Items.acceptsValue(item) {
				return false
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok || (len(s.Enum) > 0 && !slices.Contains(s.Enum, text)) {
			return false
		}
	}
	return true
}
