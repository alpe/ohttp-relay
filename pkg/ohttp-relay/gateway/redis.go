package gateway

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisGatewaySource implements URLSource by fetching gateway URLs from Redis.
// Lookup currently uses exact host key match.
type RedisGatewaySource struct {
	rdb     *redis.Client
	timeout time.Duration
}

// NewRedisGatewaySource creates a Redis client and returns a URLSource bound to it.
func NewRedisGatewaySource(client *redis.Client, ttl time.Duration) (*RedisGatewaySource, error) {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	return &RedisGatewaySource{
		rdb:     client,
		timeout: ttl,
	}, nil
}

func (r *RedisGatewaySource) TargetURL(ctx context.Context, host string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(host))
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	if v, err := r.rdb.HGet(ctx, h, "url").Result(); err == nil {
		return v, nil
	} else if !errors.Is(err, redis.Nil) {
		return "", err
	}

	return "", nil
}
