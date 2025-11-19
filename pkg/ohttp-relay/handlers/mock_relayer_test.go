package handlers

import (
	"context"
	"net/http"
)

// mockRelayer is a simple mock for testing.
type mockRelayer struct {
	relayFunc func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error)
}

func (m *mockRelayer) Relay(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
	if m.relayFunc != nil {
		return m.relayFunc(ctx, host, body, contentType, method)
	}
	return nil, nil
}
