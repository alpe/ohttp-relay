# Echo Gateway

Simple oHTTP gateway implementation for testing purpose.
Returns the request headers and body in JSON format. 

## Configuration

- `GATEWAY_INTERNAL_API_KEY`: if set, the gateway requires incoming requests to include header `X-Internal-Api-Key` with the exact value. The health endpoint `/healthz` and the well-known key config path `/.well-known/ohttp-configs` remain publicly accessible. Metrics are exposed on the separate metrics listener controlled by `METRICS_ADDR`.