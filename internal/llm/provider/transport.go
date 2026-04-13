package provider

import (
	"bytes"
	"io"
	"net/http"
)

type contextKey string

const (
	CaptureKey contextKey = "payload_capture"
)

// PayloadCapture stores the captured request body
type PayloadCapture struct {
	Body []byte
}

// PayloadCaptureTransport wraps http.RoundTripper to capture the request body
type PayloadCaptureTransport struct {
	Base http.RoundTripper
}

func (t *PayloadCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		// Check if capture is requested in the context
		if pc, ok := req.Context().Value(CaptureKey).(*PayloadCapture); ok {
			bodyBytes, err := io.ReadAll(req.Body)
			if err == nil {
				pc.Body = bodyBytes
				// Restore body for the actual request
				req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}
		}
	}

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
