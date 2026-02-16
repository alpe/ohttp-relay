package config

import (
	"testing"
)

func FuzzParseDomainMap(f *testing.F) {
	// Seed with valid examples
	f.Add("domain:https://gateway/relay")
	f.Add("d1:u1,d2:u2")
	f.Add("foo=bar")
	f.Add("")
	f.Add("::,=")

	f.Fuzz(func(t *testing.T, spec string) {
		// We just want to ensure it doesn't panic
		_, _ = parseDomainMap(spec)
	})
}
