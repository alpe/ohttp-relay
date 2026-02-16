FROM golang:1.26-bookworm@sha256:eae3cdfa040d0786510a5959d36a836978724d03b34a166ba2e0e198baac9196 AS builder
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
