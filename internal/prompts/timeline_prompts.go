package prompts

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"claude_bot/internal/model"

	"github.com/mattn/go-mastodon"
)

func BuildAssistantAnalysisPrompt(statuses []*mastodon.Status, userRequest string) string {
	var sb strings.Builder
	sb.WriteString(Templates.AssistantAnalysis.Instruction)
	sb.WriteString("【分析対象の投稿】\n")

	re := regexp.MustCompile(`<[^>]*>`)

	for _, status := range statuses {
		content := re.ReplaceAllString(string(status.Content), "")
		createdAt := status.CreatedAt.Format("2006-01-02 15:04:05")
		sb.WriteString(fmt.Sprintf("- [%s] (ID: %s): %s\n", createdAt, status.ID, content))
	}

	sb.WriteString("\n【ユーザーからのリクエスト】\n")
	if userRequest != "" {
		sb.WriteString(userRequest)
	} else {
		sb.WriteString("指定範囲の主な論点をまとめ、投稿から確認できる問題や見落としがあれば挙げてください。")
	}

	sb.WriteString(Templates.AssistantAnalysis.OutputFormat)

	return sb.String()
}

func BuildAutoPostPrompt(facts []model.Fact) string {
	var factList strings.Builder
	var source string

	for _, fact := range facts {
		factList.WriteString(fmt.Sprintf("- %s: %v\n", fact.Key, fact.Value))
		if source == "" {
			source = fact.TargetUserName
		}
	}

	return fmt.Sprintf(Templates.AutoPost, source, factList.String())
}

func BuildDailySummaryPrompt(statuses []*mastodon.Status, targetDateStr, userRequest string, loc *time.Location) string {
	var sb strings.Builder
	tzName := "指定タイムゾーン"
	if loc != nil {
		tzName = loc.String()
	}
	sb.WriteString(fmt.Sprintf(Templates.DailySummary.Header, targetDateStr, tzName))
	sb.WriteString("【投稿ログ】\n")

	re := regexp.MustCompile(`<[^>]*>`)

	for _, status := range statuses {
		content := re.ReplaceAllString(string(status.Content), "")
		createdAt := status.CreatedAt.In(loc).Format("15:04")
		sb.WriteString(fmt.Sprintf("- [%s]: %s\n", createdAt, content))
	}

	if userRequest != "" {
		sb.WriteString("\n【ユーザーからのリクエスト】\n")
		sb.WriteString(userRequest + "\n")
	}

	sb.WriteString(Templates.DailySummary.Instruction)

	return sb.String()
}
