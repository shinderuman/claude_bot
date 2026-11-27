package prompts

import (
	"fmt"
	"math"
	"strings"

	"claude_bot/internal/config"
)

func BuildSystemPrompt(cfg *config.Config, sessionSummary, relevantFacts, botProfile string, includeCharacterPrompt bool, priority float64) string {
	var prompt strings.Builder
	prompt.WriteString(Messages.System.Base)

	if cfg.BotUsername != "" {
		fmt.Fprintf(&prompt, "\nあなたのMastodon IDは @%s です。\n\n", cfg.BotUsername)
	}

	if includeCharacterPrompt {
		factWeight := int(math.Round((1.0 - priority) * 100))
		charWeight := int(math.Round(priority * 100))

		weightInstruction := fmt.Sprintf(`【応答バランス指示】
あなたのキャラクター設定: %d%% / データベースの事実情報: %d%%

この数値に応じて応答を調整してください：
- 数値は、応答のキャラクター性と、関連する記憶候補をどの程度詳しく取り上げるかのバランスに使ってください。
- どの値でも、記憶候補を参照する条件は「【データベースの記憶候補】」の指示に従ってください。自己認識（一人称、名前、性格、経歴、能力）は常にキャラクター設定に従ってください。

現在の設定では、キャラクター設定を%d%%の強さで維持してください。事実情報に含まれる他者の経歴やスペックを自分と混同しないよう厳重に注意してください。`, charWeight, factWeight, charWeight)

		prompt.WriteString(weightInstruction + "\n\n")
	}

	characterPart := ""
	if includeCharacterPrompt {
		var sb strings.Builder
		sb.WriteString(cfg.CharacterPrompt)
		sb.WriteString("\n\n")

		if botProfile != "" {
			truncatedProfile := truncateFactsByPriority(botProfile, priority, includeCharacterPrompt)
			sb.WriteString("【現在の自己認識（学習済みプロファイル）】\n")
			sb.WriteString(truncatedProfile)
			sb.WriteString("\n\n")
		}
		if cfg.MaxPostChars > 0 {
			fmt.Fprintf(&sb, Messages.System.ReplyLength, cfg.MaxPostChars)
		}
		sb.WriteString(Messages.System.ReplyFormatting)
		characterPart = sb.String()
	}

	factsPart := ""
	if relevantFacts != "" {
		factsContent := truncateFactsByPriority(relevantFacts, priority, includeCharacterPrompt)
		factsPart = Messages.System.KnowledgeBase + factsContent + "\n\n"
	}

	sessionPart := ""
	if sessionSummary != "" {
		sessionPart = Messages.System.SessionSummary + sessionSummary + "\n\n"
	}

	prompt.WriteString(sessionPart)

	if priority < 0.5 {
		if characterPart != "" {
			prompt.WriteString(characterPart)
			prompt.WriteString("\n\n")
		}
		if factsPart != "" {
			prompt.WriteString(factsPart)
			prompt.WriteString("\n\n")
		}
	} else {
		if factsPart != "" {
			prompt.WriteString(factsPart)
			prompt.WriteString("\n\n")
		}
		if characterPart != "" {
			prompt.WriteString(characterPart)
			prompt.WriteString("\n\n")
		}
	}

	return prompt.String()
}
