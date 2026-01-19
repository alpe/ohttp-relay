package gateway

import (
	"context"
)

// URLSource provides a way to resolve an upstream gateway URL for a given host.
// Implementations may use static maps, Redis, or other backends.
type URLSource interface {
	TargetURL(ctx context.Context, host string) (string, error)
}

var _ URLSource = TargetURLFn(nil)

type TargetURLFn func(ctx context.Context, host string) (string, error)

func (t TargetURLFn) TargetURL(ctx context.Context, host string) (string, error) {
	return t(ctx, host)
}

// FallbackSource return default gateway URL if the other source returns an empty string.
func FallbackSource(defaultGatewayURL string, other URLSource) URLSource {
	return TargetURLFn(func(ctx context.Context, host string) (string, error) {
		gw, err := other.TargetURL(ctx, host)
		if err != nil {
			return "", err
		}
		if gw != "" {
			return gw, nil
		}
		return defaultGatewayURL, nil
	})
}

// ChainSources returns a URLSource that tries each source in order until a non-empty URL is found.
func ChainSources(sources ...URLSource) URLSource {
	return TargetURLFn(func(ctx context.Context, host string) (string, error) {
		for _, s := range sources {
			gw, err := s.TargetURL(ctx, host)
			if err != nil {
				return "", err
			}
			if gw != "" {
				return gw, nil
			}
		}
		return "", nil
	})
}

// KeyConfigSource provides a way to fetch key config for a given host.
type KeyConfigSource interface {
	KeyConfig(ctx context.Context, host string) ([]byte, error)
}

var _ KeyConfigSource = KeyConfigSourceFn(nil)

type KeyConfigSourceFn func(ctx context.Context, host string) ([]byte, error)

func (k KeyConfigSourceFn) KeyConfig(ctx context.Context, host string) ([]byte, error) {
	return k(ctx, host)
}
