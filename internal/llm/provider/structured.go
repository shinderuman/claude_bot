package provider

type StructuredSchema struct {
	Type       string                       `json:"type"`
	Properties map[string]*StructuredSchema `json:"properties,omitempty"`
	Items      *StructuredSchema            `json:"items,omitempty"`
	Required   []string                     `json:"required,omitempty"`
	Enum       []string                     `json:"enum,omitempty"`
}
