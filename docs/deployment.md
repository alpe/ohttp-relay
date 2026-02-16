# Deployment

## Prerequisites
- **Docker**: For containerized deployment.
- **Go 1.22+**: For local builds.

## Building via Makefile

The project includes a `Makefile` to simplify building and testing.

```bash
# Build all binaries to ./bin
make build-all

# Run all tests
make test-all
```

## Running with Docker Compose

A `docker-compose.yml` is provided for running a complete local stack, including:
- **ohttp-relay**: The relay service.
- **echo-gateway**: A test gateway.
- **envoy**: Envoy proxy frontend.
- **redis**: For dynamic configuration.

### Start the Stack
```bash
make e2e-up
# OR
docker compose up -d
```

### Run E2E Tests
To run the end-to-end tests against the running stack:
```bash
make e2e-test
```

### Stop the Stack
```bash
make e2e-down
```

## Production Deployment
For production, standard Docker deployment is recommended. The service exposes gRPC ports that should be proxied by an edge load balancer (like Envoy) that handles TLS termination and HTTP/2.

### Health Checks
Configure your orchestrator (Kubernetes, AWS ECS, etc.) to check the gRPC health port (default `9007`).
