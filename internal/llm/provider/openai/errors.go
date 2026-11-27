package openai

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	errStreamIncomplete   = errors.New("OpenAI応答が完了しませんでした (response.incomplete)")
	errStreamUnterminated = errors.New("OpenAI応答ストリームがresponse.completedで終了しませんでした")
)

type ResponsesError struct {
	StatusCode int
	Event      string
	Code       string
	Message    string
	Param      string
	RequestID  string
	cause      error
}

func (e *ResponsesError) Error() string {
	var message strings.Builder
	message.WriteString("OpenAI Responses APIエラー")
	if e.StatusCode != 0 {
		fmt.Fprintf(&message, ": HTTP %d", e.StatusCode)
	}
	if e.Event != "" {
		fmt.Fprintf(&message, " %s", e.Event)
	}
	if e.Code != "" {
		fmt.Fprintf(&message, " %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&message, ": %s", e.Message)
	}
	if e.cause != nil {
		fmt.Fprintf(&message, ": %v", e.cause)
	}
	if e.Param != "" {
		fmt.Fprintf(&message, " (param: %s)", e.Param)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&message, " (request_id: %s)", e.RequestID)
	}
	return message.String()
}

func (e *ResponsesError) Unwrap() error {
	return e.cause
}

func (e *ResponsesError) statusForRecovery() int {
	if e.StatusCode == http.StatusOK {
		return statusCodeForErrorCode(e.Code)
	}
	return e.StatusCode
}

func statusCodeForErrorCode(code string) int {
	switch code {
	case "subscription_sharing_usage_limit_exceeded":
		return 429
	case "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		return 503
	case "subscription_sharing_user_not_eligible", "subscription_sharing_route_not_supported",
		"chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context":
		return 403
	case "subscription_sharing_unsupported_capability":
		return 400
	case "subscription_sharing_invalid_user":
		return 401
	default:
		return 0
	}
}
