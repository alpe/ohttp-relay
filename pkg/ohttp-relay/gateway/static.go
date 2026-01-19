package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// StaticMapURLSource returns a source that returns the value of the given map for each host.
func StaticMapURLSource(source map[string]string) URLSource {
	return TargetURLFn(func(_ context.Context, host string) (string, error) {
		return source[host], nil
	})
}

func StaticMapKeyConfigSource(source map[string][]byte) KeyConfigSource {
	return KeyConfigSourceFn(func(ctx context.Context, host string) ([]byte, error) {
		return source[host], nil
	})
}

const contentTypeOHTTPKeys = `application/ohttp-keys`
const ohttpConfigsPath = "/.well-known/ohttp-configs"

func NewHttpKeyConfigSource(h *http.Client, gwSource URLSource) KeyConfigSource {
	return KeyConfigSourceFn(func(ctx context.Context, host string) ([]byte, error) {
		targetURL, err := gwSource.TargetURL(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("get gateway url for host %q: %w", host, err)
		}
		if targetURL == "" {
			return nil, fmt.Errorf("no gateway url for host %q", host)
		}
		gatewayURL := targetURL + ohttpConfigsPath
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Accept", contentTypeOHTTPKeys)
		req.Header.Set("User-Agent", "ohttp-relay")
		fmt.Printf("fetching key config from %s", gatewayURL)
		rsp, err := h.Do(req)
		if err != nil {
			return nil, err
		}
		defer rsp.Body.Close() // nolint: errcheck
		if rsp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d", rsp.StatusCode)
		}
		bz, err := io.ReadAll(rsp.Body)
		return bz, nil
	})
}
