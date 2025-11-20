package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"

	envoyCorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestProcess_happy(t *testing.T) {
	relayer := &mockRelayer{
		relayFunc: func(ctx context.Context, host, path string, body []byte, contentType string, method string) (*http.Response, error) {
			// Return a mock response
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{ContentTypeOHTTPRes}},
				Body:       io.NopCloser(bytes.NewReader([]byte("opaque-res"))),
			}, nil
		},
	}
	srv := NewServer(relayer, 10*1024*1024) // 10MB limit

	ctx := t.Context()
	fps := &fakeProcessServer{ctx: ctx, recvCh: make(chan *extProcPb.ProcessingRequest, 3)}

	// Send headers and one EOS body
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_RequestHeaders{RequestHeaders: &extProcPb.HttpHeaders{Headers: &envoyCorev3.HeaderMap{Headers: []*envoyCorev3.HeaderValue{
		{Key: "method", RawValue: []byte(http.MethodPost)},
		{Key: ":authority", RawValue: []byte("example.test")},
		{Key: "content-type", RawValue: []byte(ContentTypeOHTTPReq)},
	}}}}}
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_RequestBody{RequestBody: &extProcPb.HttpBody{Body: []byte("opaque"), EndOfStream: true}}}
	close(fps.recvCh)

	err := srv.Process(fps)
	require.NoError(t, err)

	// Expect a single ImmediateResponse with OK and message/ohttp-res
	require.Len(t, fps.sends, 1)
	ir := getImmediateResp(t, fps.sends[0])
	assert.Equal(t, envoyTypePb.StatusCode_OK, ir.GetStatus().GetCode())
	assert.Equal(t, []byte("opaque-res"), ir.Body)
	// Check content-type header
	var ct string
	for _, h := range ir.GetHeaders().GetSetHeaders() {
		if h.GetHeader().GetKey() == "content-type" {
			ct = string(h.GetHeader().GetRawValue())
			break
		}
	}
	assert.Equal(t, ContentTypeOHTTPRes, ct)
}

func TestProcess_chunkedBody_noEarlyResponse(t *testing.T) {
	relayer := &mockRelayer{
		relayFunc: func(ctx context.Context, host, path string, body []byte, contentType string, method string) (*http.Response, error) {
			// Return a mock response
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{ContentTypeOHTTPRes}},
				Body:       io.NopCloser(bytes.NewReader([]byte("ok"))),
			}, nil
		},
	}
	srv := NewServer(relayer, 10*1024*1024) // 10MB limit
	fps := &fakeProcessServer{ctx: t.Context(), recvCh: make(chan *extProcPb.ProcessingRequest, 4)}

	// headers
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_RequestHeaders{RequestHeaders: &extProcPb.HttpHeaders{Headers: &envoyCorev3.HeaderMap{Headers: []*envoyCorev3.HeaderValue{
		{Key: "method", RawValue: []byte(http.MethodPost)},
		{Key: "host", RawValue: []byte("h")},
		{Key: "content-type", RawValue: []byte(ContentTypeOHTTPReq)},
	}}}}}
	// first chunk (no EOS)
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_RequestBody{RequestBody: &extProcPb.HttpBody{Body: []byte("part1"), EndOfStream: false}}}
	// second chunk with EOS
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_RequestBody{RequestBody: &extProcPb.HttpBody{Body: []byte("part2"), EndOfStream: true}}}
	close(fps.recvCh)

	err := srv.Process(fps)
	require.NoError(t, err)

	// Only one response after EOS
	require.Len(t, fps.sends, 1)
	ir := getImmediateResp(t, fps.sends[0])
	assert.Equal(t, envoyTypePb.StatusCode_OK, ir.GetStatus().GetCode())
}

func TestProcess_recvError(t *testing.T) {
	srv := &Server{streaming: false, relayer: &mockRelayer{}, maxRequestBodySize: 10 * 1024 * 1024}
	fps := &fakeProcessServer{ctx: t.Context(), recvCh: make(chan *extProcPb.ProcessingRequest)}
	// close channel and force Recv error
	close(fps.recvCh)
	fps.recvErr = assert.AnError

	err := srv.Process(fps)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unknown, st.Code())
	assert.Contains(t, st.Message(), "cannot receive stream request")
}

func TestProcess_contextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	srv := &Server{streaming: false, relayer: &mockRelayer{}, maxRequestBodySize: 10 * 1024 * 1024}
	fps := &fakeProcessServer{ctx: ctx, recvCh: make(chan *extProcPb.ProcessingRequest)}

	err := srv.Process(fps)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
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
		relayFunc: func(ctx context.Context, host, path string, body []byte, contentType string, method string) (*http.Response, error) {
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
