FROM golang:1.25-bookworm@sha256:5117d68695f57faa6c2b3a49a6f3187ec1f66c75d5b080e4360bfe4c1ada398c AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build static binary
RUN --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags "-s -w" -o /out/gateway-registry ./cmd/gateway-registry

FROM scratch
WORKDIR /

# Copy ca-certificates and tzdata
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=builder /out/gateway-registry /gateway-registry

USER 10001

EXPOSE 8080
ENTRYPOINT ["/gateway-registry"]
