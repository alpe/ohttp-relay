package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisGatewaySource implements Source by fetching gateway URLs from Redis.
// Lookup currently uses exact host key match.
type RedisGatewaySource struct {
	rdb     *redis.Client
	timeout time.Duration
}

type RedisOptions struct {
	Addr     string
	Password string
	DB       int
	UseTLS   bool
	Timeout  time.Duration
}

// NewRedisGatewaySource creates a Redis client and returns a Source bound to it.
func NewRedisGatewaySource(opts RedisOptions) (*RedisGatewaySource, error) {
	ro := &redis.Options{Addr: opts.Addr, Password: opts.Password, DB: opts.DB}
	if opts.UseTLS {
		ro.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client := redis.NewClient(ro)
	// quick ping with a short timeout to validate connectivity early
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	to := opts.Timeout
	if to <= 0 {
		to = 5 * time.Second
	}
	return &RedisGatewaySource{
		rdb:     client,
		timeout: to,
	}, nil
}

func (r *RedisGatewaySource) Get(host string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(host))
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	if v, err := r.rdb.Get(ctx, h).Result(); err == nil {
		return v, nil
	} else if !errors.Is(err, redis.Nil) {
		return "", err
	}

	return "", nil
}
