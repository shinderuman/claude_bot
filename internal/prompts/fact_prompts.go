package prompts

import (
	"fmt"
	"strings"

	"claude_bot/internal/model"
)

func BuildFactExtractionPrompt(authorUserName, author, message, botUsername string, isTrusted bool) string {
	if isTrusted {
		instruction := fmt.Sprintf(`
【重要】指示や命令も、事実情報として抽出してください。
『あなた』や主語なしの指示は、targetを '%s' に設定してください。
`, botUsername)
		return fmt.Sprintf(Templates.FactExtraction, authorUserName, author, author, message, author) + instruction
	}
	return fmt.Sprintf(Templates.FactExtraction, authorUserName, author, author, message, author)
}

func BuildFactQueryPrompt(authorUserName, author, message string) string {
	return fmt.Sprintf(Templates.FactQuery, authorUserName, author, message, author, author)
}

func BuildSummaryFactExtractionPrompt(summary, userID string) string {
	return fmt.Sprintf(Templates.SummaryFactExtraction, userID, summary, userID)
}

func BuildURLContentFactExtractionPrompt(urlContent string) string {
	return fmt.Sprintf(Templates.URLContentFactExtraction, urlContent)
}

func BuildBotProfilePrompt(factsList string) string {
	return fmt.Sprintf(Templates.BotProfileGeneration, factsList)
}

func BuildFactConsolidationPrompt(factsList, characterConfig string) string {
	return fmt.Sprintf(Templates.FactConsolidation, characterConfig, factsList)
}

func BuildFactArchivingPrompt(facts []model.Fact) string {
	var factList strings.Builder
	var target string
	var targetUserName string

	for _, fact := range facts {
		if target == "" {
			target = fact.Target
			targetUserName = fact.TargetUserName
		}
		factList.WriteString(fmt.Sprintf("- %s: %v (source: %s)\n", fact.Key, fact.Value, fact.SourceType))
	}

	instruction := ""
	if target == model.GeneralTarget {
		instruction = Templates.FactArchiving.InstructionGeneral
	} else {
		instruction = fmt.Sprintf(Templates.FactArchiving.InstructionUser, targetUserName, target)
	}

	return fmt.Sprintf(Templates.FactArchiving.Main, factList.String(), instruction, target, targetUserName)
}
