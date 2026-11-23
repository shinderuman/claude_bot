package collector

import (
	"sync"
	"testing"
	"time"

	"claude_bot/internal/config"
)

func TestCleanupExpiredSets(t *testing.T) {
	cfg := &config.Config{}

	fc := &FactCollector{
		config:           cfg,
		processedURLs:    sync.Map{},
		fediverseDomains: sync.Map{},
	}

	now := time.Now()
	expiredTime := now.Add(-CacheTTL - time.Hour)
	validTime := now.Add(-time.Minute)

	fc.processedURLs.Store("expired_url", expiredTime)
	fc.processedURLs.Store("valid_url", validTime)

	fc.fediverseDomains.Store("expired.com", expiredTime)
	fc.fediverseDomains.Store("valid.com", validTime)

	fc.cleanupExpiredSets(now)

	if _, ok := fc.processedURLs.Load("expired_url"); ok {
		t.Error("Expired URL should be removed")
	}
	if _, ok := fc.processedURLs.Load("valid_url"); !ok {
		t.Error("Valid URL should be kept")
	}

	if _, ok := fc.fediverseDomains.Load("expired.com"); ok {
		t.Error("Expired domain should be removed")
	}
	if _, ok := fc.fediverseDomains.Load("valid.com"); !ok {
		t.Error("Valid domain should be kept")
	}
}

func TestReserveHourlyProcessingSlotReleasesExpiredCapacity(t *testing.T) {
	collector := NewFactCollector(&config.Config{FactCollectionMaxPerHour: 2}, nil, nil)
	collector.processedTimes = []time.Time{time.Now().Add(-2 * time.Hour), time.Now().Add(-time.Minute)}

	if !collector.reserveHourlyProcessingSlot() {
		t.Fatal("expired processing slot still counted against the quota")
	}
	if collector.reserveHourlyProcessingSlot() {
		t.Fatal("collector exceeded the hourly quota")
	}
}
