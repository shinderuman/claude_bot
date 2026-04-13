package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

type mockRoundTripper struct {
	lastRequest *http.Request
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	m.lastRequest = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("ok")),
	}, nil
}

func TestPayloadCaptureTransport_RoundTrip(t *testing.T) {
	t.Run("Capture successful and body preserved", func(t *testing.T) {
		mock := &mockRoundTripper{}
		transport := &PayloadCaptureTransport{Base: mock}
		pc := &PayloadCapture{}
		
		ctx := context.WithValue(context.Background(), CaptureKey, pc)
		bodyContent := "{\"test\": \"payload\"}"
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://example.com", bytes.NewBufferString(bodyContent))
		
		_, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		
		// 1. Check if captured body matches
		if string(pc.Body) != bodyContent {
			t.Errorf("Captured body = %q, want %q", string(pc.Body), bodyContent)
		}
		
		// 2. Check if body was preserved for the next RoundTripper
		if mock.lastRequest == nil || mock.lastRequest.Body == nil {
			t.Fatal("Next RoundTripper received nil body")
		}
		receivedBody, _ := io.ReadAll(mock.lastRequest.Body)
		if string(receivedBody) != bodyContent {
			t.Errorf("Next RoundTripper received body = %q, want %q", string(receivedBody), bodyContent)
		}
	})

	t.Run("Does nothing when CaptureKey is missing", func(t *testing.T) {
		mock := &mockRoundTripper{}
		transport := &PayloadCaptureTransport{Base: mock}
		
		bodyContent := "should be skipped"
		req, _ := http.NewRequest("POST", "http://example.com", bytes.NewBufferString(bodyContent))
		
		_, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		
		// Body should still be available to mock even if not captured
		receivedBody, _ := io.ReadAll(mock.lastRequest.Body)
		if string(receivedBody) != bodyContent {
			t.Errorf("Next RoundTripper received body = %q, want %q", string(receivedBody), bodyContent)
		}
	})

	t.Run("Handles nil body gracefully", func(t *testing.T) {
		mock := &mockRoundTripper{}
		transport := &PayloadCaptureTransport{Base: mock}
		pc := &PayloadCapture{}
		
		ctx := context.WithValue(context.Background(), CaptureKey, pc)
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://example.com", nil)
		
		_, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		
		if pc.Body != nil {
			t.Errorf("Expected nil captured body, got %q", string(pc.Body))
		}
	})
}
