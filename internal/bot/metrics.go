package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

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
	// ログファイルを追記モードで開く
	// 毎回開閉することで、ファイルローテーション等への耐性を高める（簡易的）
	// 高頻度ではないためパフォーマンス影響は軽微と判断
	f, err := os.OpenFile(b.config.MetricsLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open metrics log file: %w", err)
	}
	defer f.Close()

	// Facts Metrics
	factsCount := len(b.factStore.Facts)
	var factsSize int64
	factsPath := util.GetFilePath(b.config.FactStoreFileName)
	if stat, err := os.Stat(factsPath); err == nil {
		factsSize = stat.Size()
	}

	// Sessions Metrics
	sessionCount := len(b.history.Sessions)
	var sessionSize int64
	sessionPath := util.GetFilePath(b.config.SessionFileName)
	if stat, err := os.Stat(sessionPath); err == nil {
		sessionSize = stat.Size()
	}

	entry := metricsLogEntry{
		Timestamp:         time.Now().Format(time.RFC3339),
		Level:             "info",
		Msg:               "metrics",
		BotUsername:       b.config.BotUsername,
		FactsCount:        factsCount,
		FactsSizeBytes:    factsSize,
		SessionsCount:     sessionCount,
		SessionsSizeBytes: sessionSize,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal metrics: %w", err)
	}

	if _, err := fmt.Fprintln(f, string(data)); err != nil {
		return fmt.Errorf("failed to write metrics: %w", err)
	}

	return nil
}
