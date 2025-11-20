package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/metrics"
)

var ErrNoGateway = fmt.Errorf("no gateway found")

// Relayer defines the interface for relaying requests to a gateway.
type Relayer interface {
	Relay(ctx context.Context, host, path string, body []byte, contentType string, method string) (*http.Response, error)
}

// HTTPRelayer implements Relayer using an http.Client.
type HTTPRelayer struct {
	client        *http.Client
	gatewaySource GatewaySource
}

// GatewaySource is an interface to get the gateway URL for a host.
// It matches the signature of gateway.Source.Get
type GatewaySource interface {
	Get(host string) (string, error)
}

// NewHTTPRelayer creates a new HTTPRelayer.
func NewHTTPRelayer(gatewaySource GatewaySource, timeout time.Duration) *HTTPRelayer {
	return &HTTPRelayer{
		client:        &http.Client{Timeout: timeout, Transport: http.DefaultTransport},
		gatewaySource: gatewaySource,
	}
}

// Relay forwards the request to the configured gateway.
func (r *HTTPRelayer) Relay(ctx context.Context, host, path string, body []byte, contentType string, method string) (*http.Response, error) {
	start := time.Now()
	// Determine gateway by host
	gwURL, err := r.gatewaySource.Get(host)
	if err != nil {
		return nil, fmt.Errorf("get gateway mapping for host %q: %w", host, err)
	}
	if gwURL == "" {
		return nil, fmt.Errorf("host  %q: %w", host, ErrNoGateway)
	}

	// Build a fresh HTTP request to the gateway.
	targetURL := gwURL
	if path != "" {
		targetURL = gwURL + path
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, io.NopCloser(strings.NewReader(string(body))))
	if err != nil {
		return nil, fmt.Errorf("build relay request: %w", err)
	}

	// Ensure a clean header map and set only the required content-type.
	req.Header = make(http.Header)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// Prevent the default Go User-Agent from being added.
	req.Header.Set("User-Agent", "")

	resp, err := r.client.Do(req)

	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}
	metrics.RecordGatewayRequest(ctx, statusCode, time.Since(start))

	if err != nil {
		return nil, fmt.Errorf("relay request failed: %w", err)
	}

	return resp, nil
}
