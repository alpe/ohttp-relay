package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/util/logging"
)

// Config holds the configuration for the ohttp-relay service.
type Config struct {
	GRPCPort       int
	GRPCHealthPort int
	MetricsPort    int
	MetricsAddr    string
	GatewayURLs    string

	// Redis configuration
	RedisEnable   bool
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	RedisTLS      bool

	Timeout            time.Duration
	MaxRequestBodySize int64
	LogVerbosity       int

	// Parsed field
	DomainMap map[string]string
}

// Load parses command line flags and environment variables to populate the Config.
func Load() (*Config, error) {
	c := &Config{}

	flag.IntVar(&c.GRPCPort, "grpc-port", 9006, "The gRPC port used for communicating with Envoy proxy")
	flag.IntVar(&c.GRPCHealthPort, "grpc-health-port", 9007, "The port used for gRPC liveness and readiness probes")
	flag.IntVar(&c.MetricsPort, "metrics-port", 9090, "The metrics port")
	flag.StringVar(&c.MetricsAddr, "metrics-addr", "", "Metrics listen address (if set, overrides --metrics-port)")
	flag.StringVar(&c.GatewayURLs, "gateway-urls", "", "Comma-separated domain:url mappings for OHTTP gateway, e.g. domain:https://gateway/relay,other:https://other/relay")

	flag.BoolVar(&c.RedisEnable, "redis-enable", false, "Enable Redis as dynamic gateway source")
	flag.StringVar(&c.RedisAddr, "redis-addr", "", "Redis address host:port (enables Redis source when set with --redis-enable)")
	flag.StringVar(&c.RedisPassword, "redis-password", "", "Redis password")
	flag.IntVar(&c.RedisDB, "redis-db", 0, "Redis DB number")
	flag.BoolVar(&c.RedisTLS, "redis-tls", false, "Use TLS for Redis connection")

	flag.DurationVar(&c.Timeout, "timeout", 9*time.Second, "HTTP timeout when relaying to the OHTTP gateway")
	flag.Int64Var(&c.MaxRequestBodySize, "max-request-body-size", 10*1024*1024, "Maximum allowed request body size in bytes (default 10MB)")
	flag.IntVar(&c.LogVerbosity, "v", logging.DEFAULT, "number for the log level verbosity")

	flag.Parse()

	if envPass := os.Getenv("REDIS_PASSWORD"); envPass != "" {
		c.RedisPassword = envPass
	}

	var err error
	c.DomainMap, err = parseDomainMap(c.GatewayURLs)
	if err != nil {
		return nil, fmt.Errorf("parse domain map: %w", err)
	}

	return c, nil
}

func parseDomainMap(spec string) (map[string]string, error) {
	m := map[string]string{}
	if strings.TrimSpace(spec) == "" {
		return m, nil
	}
	pairs := strings.Split(spec, ",")
	for _, p := range pairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var kv []string
		if strings.Contains(p, "=") {
			kv = strings.SplitN(p, "=", 2)
		} else {
			kv = strings.SplitN(p, ":", 2)
		}
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid mapping %q, expected domain:url or domain=url", p)
		}
		domain := strings.ToLower(strings.TrimSpace(kv[0]))
		url := strings.TrimSpace(kv[1])
		if domain == "" || url == "" {
			return nil, fmt.Errorf("invalid mapping %q, empty domain or url", p)
		}
		m[domain] = url
	}
	return m, nil
}
