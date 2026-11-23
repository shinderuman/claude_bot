package prompts

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-mastodon"
)

func BuildErrorMessagePrompt(errorDetail string) string {
	if errorDetail == "" {
		return Messages.Instruction.SystemErrorFallback
	}
	return fmt.Sprintf(Templates.ErrorMessage, errorDetail)
}

func BuildImageGenerationPrompt(userRequest string) string {
	return fmt.Sprintf(Templates.ImageGeneration, userRequest)
}

func BuildImageGenerationReplyPrompt(userMessage, characterPrompt string) string {
	return fmt.Sprintf(Templates.ImageGenerationReply, characterPrompt, userMessage)
}

func BuildImageRequestDetectionPrompt(userMessage string) string {
	return fmt.Sprintf(Templates.ImageRequestDetection, userMessage)
}

func BuildIntentClassificationPrompt(userMessage string, now time.Time, urlCandidates ...string) string {
	return fmt.Sprintf(Templates.IntentClassification, now.Format("2006-01-02 15:04:05 MST -07:00"), userMessage, strings.Join(urlCandidates, "\n"))
}

func BuildCardPrompt(card *mastodon.Card) string {
	if card == nil {
		return ""
	}
	return BuildPageReference(card.URL, card.Title, card.Description, "")
}

func BuildPageReference(url, title, description, content string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n\n[参照URL情報]\nURL: %s\n", url)
	if title != "" {
		fmt.Fprintf(&sb, "タイトル: %s\n", title)
	}
	if description != "" {
		fmt.Fprintf(&sb, "説明: %s\n", description)
	}
	if content != "" {
		fmt.Fprintf(&sb, "本文抜粋: %s\n", content)
	}
	return sb.String()
}

func BuildGemmaWrapperPrompt(systemPrompt, userMessage string) string {
	return fmt.Sprintf(Messages.System.GemmaWrapper, systemPrompt, userMessage)
}

func BuildSummaryPrompt(formattedMessages, existingSummary string) string {
	var content string
	var instruction string

	if existingSummary != "" {
		content = fmt.Sprintf("【これまでの会話要約】\n%s\n\n【新しい会話】\n%s", existingSummary, formattedMessages)
		instruction = Templates.Summary.InstructionUpdate
	} else {
		content = fmt.Sprintf("【新しい会話】\n%s", formattedMessages)
		instruction = Templates.Summary.InstructionNew
	}

	return instruction + "\n" + fmt.Sprintf(Templates.Summary.Main, content)
}
