package gateway

// Source provides a way to resolve an upstream gateway URL for a given host.
// Implementations may use static maps, Redis, or other backends.
type Source interface {
	Get(host string) (string, error)
}

var _ Source = SourceFn(nil)

type SourceFn func(host string) (string, error)

func (g SourceFn) Get(host string) (string, error) {
	return g(host)
}

// FallbackSource return default gateway URL if the other source returns an empty string.
func FallbackSource(defaultGatwwayURL string, other Source) Source {
	return SourceFn(func(host string) (string, error) {
		gw, err := other.Get(host)
		if err != nil {
			return "", err
		}
		if gw != "" {
			return gw, nil
		}
		return defaultGatwwayURL, nil
	})
}
