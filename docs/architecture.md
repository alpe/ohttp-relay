# OHTTP Relay Architecture

The `ohttp-relay` is a service designed to act as an Oblivious HTTP (OHTTP) Relay, as defined in RFC 9458. It provides privacy by preventing the Gateway (and target server) from seeing the Client's IP address.

## Core Concepts

### Oblivious HTTP (OHTTP)
OHTTP separates knowledge of "who" (Client) and "what" (Request) into two entities:
1.  **Relay** (This Service): Knows the Client's IP but cannot see the request content (encrypted).
2.  **Gateway**: Sees the decrypted request but does not know the Client's IP (sees Relay's IP).

### Components

#### 1. Relay Server
The core component that handles incoming HTTP requests.
- Accepts `POST` requests with content-type `message/ohttp-req`.
- Forwards the encrypted payload to the configured Gateway.
- Returns the Gateway's response (`message/ohttp-res`) to the Client.

#### 2. Discovery/Configuration
The Relay needs to know where to forward requests. This is handled via:
- **Static Configuration**: A map of domains to Gateway URLs provided at startup via flags.
- **Dynamic Configuration (Redis)**: Optionally, Redis can be used to dynamically store and retrieve Gateway mappings.

#### 3. Observability
- **Metrics**: Prometheus metrics are exposed on a dedicated port (default `9090`).
- **Health Checks**: gRPC health probes (liveness/readiness) on a dedicated port (default `9007`).
- **Logs**: Structured logging with configurable verbosity.

## Request Flow

1.  **Client** constructs an encapsulated OHTTP request aimed at a specific target resource.
2.  **Client** sends the outer HTTP request to the **Relay**.
3.  **Relay** receives the request:
    - Validates the `Content-Type`.
    - Looks up the destination Gateway based on the configuration (e.g., path or header instructions, though this specific implementation maps domains).
    - Strips client-identifying headers.
    - Forwards the request body to the **Gateway**.
4.  **Gateway** receives the request from the Relay, decapsulates it, forwards it to the Target, and returns the response.
5.  **Relay** receives the response from the Gateway and forwards it back to the **Client**.
