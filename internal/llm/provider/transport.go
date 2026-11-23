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

type PayloadCapture struct {
	Body []byte
}

type PayloadCaptureTransport struct {
	Base http.RoundTripper
}

func (t *PayloadCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		if pc, ok := req.Context().Value(CaptureKey).(*PayloadCapture); ok {
			bodyBytes, err := io.ReadAll(req.Body)
			if err == nil {
				pc.Body = bodyBytes
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
