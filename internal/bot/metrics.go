package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"claude_bot/internal/discovery"
	"claude_bot/internal/model"
	"claude_bot/internal/util"
)

type metricsLogEntry struct {
	Timestamp         string `json:"timestamp"`
	Level             string `json:"level"`
	Msg               string `json:"msg"`
	BotUsername       string `json:"bot_username"`
	FactsCount        int    `json:"facts_count"`
	FactsSizeBytes    int64  `json:"facts_size"`
	SessionsCount     int    `json:"sessions_count"`
	SessionsSizeBytes int64  `json:"sessions_size"`
}

type detailedMetricsLogEntry struct {
	Timestamp   string `json:"timestamp"`
	Level       string `json:"level"`
	Msg         string `json:"msg"`
	BotUsername string `json:"bot_username"`
	MetricType  string `json:"metric_type"` // "fact_stat_source" or "fact_stat_target"
	Category    string `json:"category"`
	Count       int    `json:"count"`
}

type FactStats struct {
	Total    int            `json:"total"`
	BySource map[string]int `json:"by_source"`
	ByTarget map[string]int `json:"by_target"`
}

func (b *Bot) startMetricsLogger(ctx context.Context) {
	interval := time.Duration(b.config.MetricsLogIntervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 初回ログ出力 (起動直後にも状態を知りたいため)
	if err := b.collectAndLogMetrics(); err != nil {
		log.Printf("メトリクスログ出力失敗: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.collectAndLogMetrics(); err != nil {
				log.Printf("メトリクスログ出力失敗: %v", err)
			}
		}
	}
}

func (b *Bot) collectAndLogMetrics() error {
	// 1. データ収集と計算
	allFacts := b.factStore.GetAllFacts()
	botUsernames, err := discovery.GetKnownBotUsernames()
	if err != nil {
		log.Printf("Warning: failed to get known bot usernames for metrics: %v", err)
	}

	factStats := calculateFactStats(allFacts, botUsernames)
	factsSize := calculateTotalFactsSize(allFacts)
	sessionCount, sessionSize := b.history.GetStats()

	// 2. ログファイルを開く
	f, err := os.OpenFile(util.GetFilePath(b.config.MetricsLogFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open metrics log file: %w", err)
	}
	defer f.Close() //nolint:errcheck

	timestamp := time.Now().Format(time.RFC3339)
	encoder := json.NewEncoder(f)

	// 3. 各ログを出力
	if err := writeSummaryLog(encoder, timestamp, b.config.BotUsername, len(allFacts), factsSize, sessionCount, sessionSize, factStats); err != nil {
		return fmt.Errorf("failed to write summary log: %w", err)
	}

	if err := writeDetailedStats(encoder, timestamp, b.config.BotUsername, "fact_stat_source", factStats.BySource); err != nil {
		return fmt.Errorf("failed to write source stats: %w", err)
	}

	if err := writeDetailedStats(encoder, timestamp, b.config.BotUsername, "fact_stat_target", factStats.ByTarget); err != nil {
		return fmt.Errorf("failed to write target stats: %w", err)
	}

	return nil
}

func writeSummaryLog(enc *json.Encoder, timestamp, botUsername string, factsCount int, factsSize int64, sessionCount int, sessionSize int64, factStats FactStats) error {
	entry := metricsLogEntry{
		Timestamp:         timestamp,
		Level:             "info",
		Msg:               "metrics",
		BotUsername:       botUsername,
		FactsCount:        factsCount,
		FactsSizeBytes:    factsSize,
		SessionsCount:     sessionCount,
		SessionsSizeBytes: sessionSize,
	}
	return enc.Encode(entry)
}

func calculateTotalFactsSize(facts []model.Fact) int64 {
	var size int64
	for _, f := range facts {
		size += int64(len(f.Key) + len(fmt.Sprint(f.Value)))
	}
	return size
}

func writeDetailedStats(enc *json.Encoder, timestamp, botUsername, metricType string, stats map[string]int) error {
	for category, count := range stats {
		l := detailedMetricsLogEntry{
			Timestamp:   timestamp,
			Level:       "info",
			Msg:         "detailed_fact_stat",
			BotUsername: botUsername,
			MetricType:  metricType,
			Category:    category,
			Count:       count,
		}
		if err := enc.Encode(l); err != nil {
			return fmt.Errorf("failed to encode detailed metric %s: %w", metricType, err)
		}
	}
	return nil
}

func calculateFactStats(facts []model.Fact, botUsernames []string) FactStats {
	stats := FactStats{
		Total:    len(facts),
		BySource: make(map[string]int),
		ByTarget: make(map[string]int),
	}

	// ボット判定用マップ
	isBot := make(map[string]bool)
	for _, name := range botUsernames {
		isBot[name] = true
	}

	for _, f := range facts {
		// BySource
		src := f.SourceType
		if src == "" {
			src = "unknown"
		}
		stats.BySource[src]++

		// ByTarget Aggregation
		target := f.Target
		// 優先順位: General > Assistant > Bots > Users
		if target == model.GeneralTarget {
			stats.ByTarget["general"]++
		} else if target == "assistant" {
			stats.ByTarget["assistant"]++
		} else if isBot[target] {
			stats.ByTarget[target]++
		} else {
			// その他はユーザーとして集約
			stats.ByTarget["users"]++
		}
	}

	return stats
}
