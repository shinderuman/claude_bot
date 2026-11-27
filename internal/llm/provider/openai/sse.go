package openai

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type streamEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Param    string `json:"param"`
	Response struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Param   string `json:"param"`
		} `json:"error"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
}

func consumeResponseStream(body io.Reader) (string, error) {
	reader := bufio.NewReader(body)
	var text strings.Builder
	var dataLines []string

	flush := func() (bool, error) {
		if len(dataLines) == 0 {
			return false, nil
		}
		payload := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		return dispatchStreamEvent(payload, &text)
	}

	skipLF := false
	firstLine := true
	for {
		line, readErr := readEventLine(reader, &skipLF)
		if errors.Is(readErr, io.EOF) {
			return "", errStreamUnterminated
		}
		if readErr != nil {
			return "", fmt.Errorf("応答ストリームの読み込みに失敗しました: %w", readErr)
		}
		if firstLine {
			line = strings.TrimPrefix(line, "\ufeff")
			firstLine = false
		}
		if line != "" {
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				dataLines = append(dataLines, strings.TrimPrefix(data, " "))
			}
			continue
		}

		finished, err := flush()
		if err != nil {
			return "", err
		}
		if finished {
			return text.String(), nil
		}
	}
}

func readEventLine(reader *bufio.Reader, skipLF *bool) (string, error) {
	var line strings.Builder
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if *skipLF {
			*skipLF = false
			if b == '\n' {
				continue
			}
		}
		if b == '\r' || b == '\n' {
			*skipLF = b == '\r'
			return line.String(), nil
		}
		line.WriteByte(b)
	}
}

func dispatchStreamEvent(payload string, text *strings.Builder) (bool, error) {
	var event streamEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return false, fmt.Errorf("応答ストリームのイベントを解析できません: %w", err)
	}
	switch event.Type {
	case "response.output_text.delta":
		text.WriteString(event.Delta)
		return false, nil
	case "response.completed":
		return true, nil
	case "response.failed":
		return false, &ResponsesError{
			Event:   event.Type,
			Code:    event.Response.Error.Code,
			Message: event.Response.Error.Message,
			Param:   event.Response.Error.Param,
		}
	case "response.incomplete":
		if reason := incompleteReason(event); reason != "" {
			return false, fmt.Errorf("%w: %s", errStreamIncomplete, reason)
		}
		return false, errStreamIncomplete
	case "error":
		return false, &ResponsesError{
			Event:   event.Type,
			Code:    event.Code,
			Message: event.Message,
			Param:   event.Param,
		}
	default:
		return false, nil
	}
}

func incompleteReason(event streamEvent) string {
	if event.Response.Error.Message != "" {
		return event.Response.Error.Message
	}
	return event.Response.IncompleteDetails.Reason
}
