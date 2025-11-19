package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"

	envoyCorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

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

func TestProcess_happy(t *testing.T) {
	relayer := &mockRelayer{
		relayFunc: func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
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
		relayFunc: func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
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
