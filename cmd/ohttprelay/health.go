package main

import (
	"context"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	logutil "github.com/alpe/ohttprelay/pkg/ohttprelay/util/logging"
)

type healthServer struct {
	logger logr.Logger
}

func (s *healthServer) Check(ctx context.Context, in *healthPb.HealthCheckRequest) (*healthPb.HealthCheckResponse, error) {
	// Accept ANY service name for now (align with other commands)
	s.logger.V(logutil.VERBOSE).Info("gRPC health check serving", "service", in.Service)
	return &healthPb.HealthCheckResponse{Status: healthPb.HealthCheckResponse_SERVING}, nil
}

func (s *healthServer) List(ctx context.Context, _ *healthPb.HealthListRequest) (*healthPb.HealthListResponse, error) {
	serviceHealthResponse, err := s.Check(ctx, &healthPb.HealthCheckRequest{Service: extProcPb.ExternalProcessor_ServiceDesc.ServiceName})
	if err != nil {
		return nil, err
	}

	return &healthPb.HealthListResponse{
		Statuses: map[string]*healthPb.HealthCheckResponse{
			extProcPb.ExternalProcessor_ServiceDesc.ServiceName: serviceHealthResponse,
		},
	}, nil
}

func (s *healthServer) Watch(in *healthPb.HealthCheckRequest, srv healthPb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
