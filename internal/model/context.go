package model

type ContextKey string

const (
	// ContextKeyIsProfileGeneration is used to context value to indicate if the request is for profile generation
	ContextKeyIsProfileGeneration ContextKey = "is_profile_generation"
)
