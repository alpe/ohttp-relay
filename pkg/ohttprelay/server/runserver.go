package server

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/alpe/ohttprelay/internal/runnable"
	tlsutil "github.com/alpe/ohttprelay/internal/tls"
	"github.com/alpe/ohttprelay/pkg/ohttprelay/handlers"
	"github.com/alpe/ohttprelay/pkg/ohttprelay/relay"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// ExtProcServerRunner provides methods to manage an external process server.
type ExtProcServerRunner struct {
	GrpcPort           int
	SecureServing      bool
	Timeout            time.Duration
	MaxRequestBodySize int64
	logger             logr.Logger
	Relayer            relay.Relayer
}

func NewExtProcServerRunner(port int, relayer relay.Relayer, maxBodySize int64, logger logr.Logger) *ExtProcServerRunner {
	return &ExtProcServerRunner{
		GrpcPort:           port,
		SecureServing:      true,
		MaxRequestBodySize: maxBodySize,
		logger:             logger,
		Relayer:            relayer,
	}
}

func (r *ExtProcServerRunner) Start(ctx context.Context) error {
	var srv *grpc.Server
	if r.SecureServing {
		cert, err := tlsutil.CreateSelfSignedTLSCertificate(r.logger)
		if err != nil {
			return err
		}
		creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})
		srv = grpc.NewServer(grpc.Creds(creds))
	} else {
		srv = grpc.NewServer()
	}
	extProcPb.RegisterExternalProcessorServer(
		srv,
		handlers.NewServer(r.Relayer, r.MaxRequestBodySize),
	)

	// Forward to the gRPC runnable.
	return runnable.GRPCServer("ext-proc", srv, r.GrpcPort)(ctx)
}
