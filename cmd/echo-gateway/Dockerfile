FROM golang:1.25-bookworm@sha256:f4490d7b261d73af4543c46ac6597d7d101b6e1755bcdd8c5159fda7046b6b3e AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build static binary
RUN --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -mod=readonly  -trimpath -ldflags "-s -w" -o /out/echo-gateway ./cmd/echo-gateway

FROM scratch
WORKDIR /

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/echo-gateway /echo-gateway

USER 10001

EXPOSE 8888
ENTRYPOINT ["/echo-gateway"]
