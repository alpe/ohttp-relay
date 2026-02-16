# Configuration

The `ohttp-relay` service is configured using command-line flags or environment variables.

## Connection & Ports

| Flag               | Default | Description                                  |
| ------------------ | ------- | -------------------------------------------- |
| `--grpc-port`      | `9006`  | gRPC port for OHTTP traffic (Envoy -> Relay) |
| `--grpc-health-port` | `9007`  | gRPC port for health checks (liveness/readiness) |
| `--metrics-port`   | `9090`  | HTTP port for Prometheus metrics             |
| `--metrics-addr`   | `""`    | Address for metrics (overrides port if set)  |

## Gateway Routing

The relay needs to map incoming requests to destination OHTTP Gateways.

### Static Configuration
Use `--gateway-urls` to define a static map of domains to Gateway URLs.

**Format**: `domain:url` or `domain=url`. Multiple mappings are comma-separated.

```bash
# Example
./ohttp-relay --gateway-urls="example.com:https://gateway.example.com,test:http://localhost:8888"
```

### Dynamic Configuration (Redis)
Enable Redis to fetch gateway URLs dynamically.

| Flag               | Default | Description                                            |
| ------------------ | ------- | ------------------------------------------------------ |
| `--redis-enable`   | `false` | Enable Redis as a configuration source                 |
| `--redis-addr`     | `""`    | Redis address (`host:port`)                            |
| `--redis-password` | `""`    | Redis password (can also use `REDIS_PASSWORD` env var) |
| `--redis-db`       | `0`     | Redis DB number                                        |
| `--redis-tls`      | `false` | Enable TLS for Redis connection                        |

## Tuning

| Flag                      | Default | Description                                         |
| ------------------------- | ------- | --------------------------------------------------- |
| `--timeout`               | `9s`    | HTTP timeout for relaying requests to the Gateway   |
| `--max-request-body-size` | `10MB`  | Maximum allowed size for request body in bytes      |
| `--v`                     | `0`     | Log verbosity level                                 |
