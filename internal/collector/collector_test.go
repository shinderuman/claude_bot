package collector

import (
	"sync"
	"testing"
	"time"

	"claude_bot/internal/config"
)

// TestCleanupExpiredSets verifies that expired items are removed and valid items are kept
func TestCleanupExpiredSets(t *testing.T) {
	// Setup
	cfg := &config.Config{}

	fc := &FactCollector{
		config:           cfg,
		processedURLs:    sync.Map{},
		fediverseDomains: sync.Map{},
	}

	now := time.Now()
	expiredTime := now.Add(-CacheTTL - time.Hour)
	validTime := now.Add(-time.Minute)

	// Add test data
	fc.processedURLs.Store("expired_url", expiredTime)
	fc.processedURLs.Store("valid_url", validTime)

	fc.fediverseDomains.Store("expired.com", expiredTime)
	fc.fediverseDomains.Store("valid.com", validTime)

	// Execute cleanup
	fc.cleanupExpiredSets(now)

	// Verify URLs
	if _, ok := fc.processedURLs.Load("expired_url"); ok {
		t.Error("Expired URL should be removed")
	}
	if _, ok := fc.processedURLs.Load("valid_url"); !ok {
		t.Error("Valid URL should be kept")
	}

	// Verify Domains
	if _, ok := fc.fediverseDomains.Load("expired.com"); ok {
		t.Error("Expired domain should be removed")
	}
	if _, ok := fc.fediverseDomains.Load("valid.com"); !ok {
		t.Error("Valid domain should be kept")
	}
}
