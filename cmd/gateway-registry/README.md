# Gateway Registry

Rest service to register and provision oHTTP gateways for the relay.

Create a new gateway:

```sh
curl -X POST http://localhost:8080/api/gateway \
-H "Content-Type: application/json" \
-d '{
"user_id": "foo",
"url": "https://example.com/gateway"
}'
```

Example response:

```json
{
  "id": "sunny-meadow-42",
  "user_id": "",
  "url": "https://example.com/gateway",
  "expiryDate": "2025-11-16T00:24:00Z"
}
```

The `id` is an auto-generated, readable, positive name that you can use to retrieve the gateway later.

Get gateway detail

```sh
curl http://localhost:8080/api/gateway/sunny-meadow-42
```