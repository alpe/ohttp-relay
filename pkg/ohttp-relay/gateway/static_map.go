package gateway

// StaticMapSource returns a source that returns the value of the given map for each host.
func StaticMapSource(source map[string]string) Source {
	return SourceFn(func(host string) (string, error) {
		return source[host], nil
	})
}
