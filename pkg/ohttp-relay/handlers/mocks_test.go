package handlers

import (
	"context"
	"io"
	"sync"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type stubStream struct {
	grpc.ServerStream
	ctx      context.Context
	recvChan chan *extProcPb.ProcessingRequest
	sendChan chan *extProcPb.ProcessingResponse
}

func (m *stubStream) Context() context.Context {
	return m.ctx
}

func (m *stubStream) Recv() (*extProcPb.ProcessingRequest, error) {
	req, ok := <-m.recvChan
	if !ok {
		return nil, io.EOF
	}
	return req, nil
}

func (m *stubStream) Send(resp *extProcPb.ProcessingResponse) error {
	m.sendChan <- resp
	return nil
}

// fakeProcessServer is a minimal implementation of extProc server stream used for tests.
type fakeProcessServer struct {
	ctx    context.Context
	recvCh chan *extProcPb.ProcessingRequest
	// if recvCh is closed and recvErr is non-nil, Recv returns this error; otherwise io.EOF
	recvErr error

	mu    sync.Mutex
	sends []*extProcPb.ProcessingResponse
}

func (f *fakeProcessServer) Context() context.Context { return f.ctx }

func (f *fakeProcessServer) Recv() (*extProcPb.ProcessingRequest, error) {
	req, ok := <-f.recvCh
	if !ok {
		if f.recvErr != nil {
			return nil, f.recvErr
		}
		return nil, io.EOF
	}
	return req, nil
}

func (f *fakeProcessServer) Send(resp *extProcPb.ProcessingResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, resp)
	return nil
}

// grpc.ServerStream methods (no-ops for tests)
func (f *fakeProcessServer) SetHeader(md metadata.MD) error  { return nil }
func (f *fakeProcessServer) SendHeader(md metadata.MD) error { return nil }
func (f *fakeProcessServer) SetTrailer(md metadata.MD)       {}
func (f *fakeProcessServer) SendMsg(m interface{}) error     { return nil }
func (f *fakeProcessServer) RecvMsg(m interface{}) error     { return nil }

var _ extProcPb.ExternalProcessor_ProcessServer = (*fakeProcessServer)(nil)
var _ grpc.ServerStream = (*fakeProcessServer)(nil)

// MockStream implements extProcPb.ExternalProcessor_ProcessServer
type MockStream struct {
	mock.Mock
	grpc.ServerStream
	ctx context.Context
}

func (m *MockStream) Context() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m *MockStream) Send(resp *extProcPb.ProcessingResponse) error {
	args := m.Called(resp)
	return args.Error(0)
}

func (m *MockStream) Recv() (*extProcPb.ProcessingRequest, error) {
	args := m.Called()
	if req := args.Get(0); req != nil {
		return req.(*extProcPb.ProcessingRequest), args.Error(1)
	}
	return nil, args.Error(1)
}
