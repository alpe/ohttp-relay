# OHTTP Relay

[![Go Report Card](https://goreportcard.com/badge/github.com/alpe/ohttp-relay)](https://goreportcard.com/report/github.com/alpe/ohttp-relay)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

The **OHTTP Relay** is an [Envoy External Processor (ext_proc)](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/ext_proc_filter) that enables privacy-preserving communication by forwarding [Oblivious HTTP](https://datatracker.ietf.org/doc/rfc9458/) encapsulated requests to a configured gateway.
It acts strictly as a relay: it does not decrypt or interpret the payload, ensuring that the relay sees only the source IP and the gateway sees only the encrypted message.

## Documentation

* [Architecture](docs/architecture.md) - high-level overview
* [Configuration](docs/configuration.md) - flags and options
* [Deployment](docs/deployment.md) - how to build and run
* [RFC9458](https://www.ietf.org/rfc/rfc9458.html) - Oblivious HTTP

> **Official Documentation & Demo:** [orelay.dev](https://orelay.dev)

## Features

- **OHTTP Encapsulation Support**: Handles `message/ohttp-req` and `message/ohttp-res` content types.
- **Envoy Integration**: Designed to work seamlessly with Envoy via the `ext_proc` filter.
- **Domain Mapping**: Routes requests to different OHTTP Gateways based on the incoming request's authority/host.
- **Redis Integration**: Optional dynamic configuration of gateway mappings via Redis.
- **Prometheus Metrics**: Exposes operational metrics for monitoring.

## Getting Started

### Prerequisites

- Go 1.25+
- [Envoy Proxy](https://www.envoyproxy.io/) (if running end-to-end)

### Build

```bash
make build
```

### Run

```bash
# Run with static mappings
./bin/ohttprelay \
  --grpc-port=9006 \
  --gateway-urls="example.com:https://gateway.example.com/relay" \
  -v=1
```

### Configuration Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--grpc-port` | Port for gRPC communication with Envoy | `9006` |
| `--metrics-port` | Port for Prometheus metrics | `9090` |
| `--gateway-urls` | Comma-separated `domain:url` mappings | `""` |

| `--timeout` | Timeout for upstream gateway requests | `9s` |
| `--redis-enable` | Enable Redis for dynamic config | `false` |


## Envoy Configuration

To use the OHTTP Relay with Envoy, configure the `ext_proc` filter in your Envoy configuration:

```yaml
http_filters:
- name: envoy.filters.http.ext_proc
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
    grpc_service:
      envoy_grpc:
        cluster_name: ohttp_relay
    processing_mode:
      request_header_mode: SEND
      request_body_mode: BUFFERED
      request_trailer_mode: SKIP
      response_header_mode: SKIP
      response_body_mode: SKIP

clusters:
- name: ohttp_relay
  type: STRICT_DNS
  connect_timeout: 1s
  http2_protocol_options: {}
  load_assignment:
    cluster_name: ohttp_relay
    endpoints:
    - lb_endpoints:
      - endpoint:
          address:
            socket_address:
              address: 127.0.0.1
              port_value: 9006
```



## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the Apache 2.0 License - see the [LICENSE](LICENSE) file for details.
