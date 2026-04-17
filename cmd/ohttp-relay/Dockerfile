FROM golang:1.26-bookworm@sha256:8d238eb7b2ac0924a13c0d46c290c986bd3a99fd09b32e2714834906f1dc149b AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build static binary
RUN --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -mod=readonly  -trimpath -ldflags "-s -w" -o /out/ohttp-relay ./cmd/ohttp-relay

FROM scratch
WORKDIR /

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/ohttp-relay /ohttp-relay

USER 10001

EXPOSE 3500
ENTRYPOINT ["/ohttp-relay"]
