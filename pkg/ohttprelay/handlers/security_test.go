package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

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

func TestProcess_BodySizeLimit(t *testing.T) {
	// Setup server with 10 byte limit
	srv := NewServer(&mockRelayer{}, 10)

	stream := new(MockStream)
	stream.ctx = context.Background()

	// Sequence of events:
	// 1. Receive Headers (start)
	// 2. Receive Body chunk 1 (5 bytes) - OK
	// 3. Receive Body chunk 2 (6 bytes) - Total 11 bytes > 10 -> Error

	// 1. Headers
	stream.On("Recv").Return(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{},
		},
	}, nil).Once()

	// 2. Body chunk 1
	stream.On("Recv").Return(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestBody{
			RequestBody: &extProcPb.HttpBody{
				Body: []byte("12345"),
			},
		},
	}, nil).Once()

	// 3. Body chunk 2
	stream.On("Recv").Return(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestBody{
			RequestBody: &extProcPb.HttpBody{
				Body: []byte("123456"),
			},
		},
	}, nil).Once()

	// Expect ImmediateResponse with 413
	stream.On("Send", mock.MatchedBy(func(resp *extProcPb.ProcessingResponse) bool {
		ir, ok := resp.Response.(*extProcPb.ProcessingResponse_ImmediateResponse)
		if !ok {
			return false
		}
		return ir.ImmediateResponse.Status.Code == envoyTypePb.StatusCode_PayloadTooLarge
	})).Return(nil).Once()

	// After error, Process should continue or return?
	// The implementation uses `goto SendResponses` which sends response and loops.
	// So we need to mock subsequent Recv to return EOF or close to stop the loop.
	stream.On("Recv").Return(nil, io.EOF)

	err := srv.Process(stream)
	// Process returns nil on EOF
	assert.NoError(t, err)

	stream.AssertExpectations(t)
}

func TestForwardAndRespond_ErrorSanitization(t *testing.T) {
	// Relayer that returns an error
	relayer := &mockRelayer{
		relayFunc: func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
			return nil, errors.New("internal sensitive error")
		},
	}

	srv := NewServer(relayer, 1024)
	st := &streamState{host: "example.com", httpMethod: http.MethodPost}

	resps, err := srv.forwardAndRespond(context.Background(), logr.Discard(), st)
	require.NoError(t, err)
	require.Len(t, resps, 1)

	ir := resps[0].Response.(*extProcPb.ProcessingResponse_ImmediateResponse).ImmediateResponse

	// Should be 502 Bad Gateway
	assert.Equal(t, envoyTypePb.StatusCode_BadGateway, ir.Status.Code)

	// Body should be generic "Bad Gateway", NOT "internal sensitive error"
	assert.Equal(t, "Bad Gateway", string(ir.Body))
}
