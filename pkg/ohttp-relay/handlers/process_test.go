package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/gateway"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/metrics"
	envoyCorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// initialize metrics before running tests to avoid panics
var _ = metrics.InitMetrics()

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
	srv := NewServer(relayer, gateway.StaticMapKeyConfigSource(map[string][]byte{}), 10*1024*1024, nil) // 10MB limit

	ctx := t.Context()
	fps := &fakeProcessServer{ctx: ctx, recvCh: make(chan *extProcPb.ProcessingRequest, 3)}

	// Send headers and one EOS body
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_RequestHeaders{RequestHeaders: &extProcPb.HttpHeaders{Headers: &envoyCorev3.HeaderMap{Headers: []*envoyCorev3.HeaderValue{
		{Key: "method", RawValue: []byte(http.MethodPost)},
		{Key: ":authority", RawValue: []byte("example.test")},
		{Key: "content-type", RawValue: []byte(ContentTypeOHTTPReq)},
		{Key: "content-length", RawValue: []byte("6")},
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
	srv := NewServer(relayer, gateway.StaticMapKeyConfigSource(map[string][]byte{}), 10*1024*1024, nil) // 10MB limit
	fps := &fakeProcessServer{ctx: t.Context(), recvCh: make(chan *extProcPb.ProcessingRequest, 4)}

	// headers
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_RequestHeaders{RequestHeaders: &extProcPb.HttpHeaders{Headers: &envoyCorev3.HeaderMap{Headers: []*envoyCorev3.HeaderValue{
		{Key: "method", RawValue: []byte(http.MethodPost)},
		{Key: "host", RawValue: []byte("h")},
		{Key: "content-type", RawValue: []byte(ContentTypeOHTTPReq)},
		{Key: "content-length", RawValue: []byte("10")},
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
	srv := NewServer(&mockRelayer{}, gateway.StaticMapKeyConfigSource(map[string][]byte{}), 10, nil)

	stream := new(MockStream)
	stream.ctx = context.Background()

	// Sequence of events:
	// 1. Receive Headers (start)
	// 2. Receive Body chunk 1 (5 bytes) - OK
	// 3. Receive Body chunk 2 (6 bytes) - Total 11 bytes > 10 -> Error

	// 1. Headers
	stream.On("Recv").Return(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{
				Headers: &envoyCorev3.HeaderMap{
					Headers: []*envoyCorev3.HeaderValue{
						{Key: "method", RawValue: []byte("POST")},
						{Key: "content-length", RawValue: []byte("11")},
					},
				},
			},
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

	srv := NewServer(relayer, gateway.StaticMapKeyConfigSource(map[string][]byte{}), 1024, nil)
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

func TestGetRespondKeyConfig(t *testing.T) {
	// Setup
	recvChan := make(chan *extProcPb.ProcessingRequest, 1)
	sendChan := make(chan *extProcPb.ProcessingResponse, 1)
	stream := &stubStream{
		ctx:      context.Background(),
		recvChan: recvChan,
		sendChan: sendChan,
	}

	ks := gateway.KeyConfigSourceFn(func(context.Context, string) ([]byte, error) {
		return []byte("mock-key-config"), nil
	})
	srv := NewServer(nil, ks, 1024, nil)

	// Simulate request with :method GET
	reqHeaders := &extProcPb.ProcessingRequest_RequestHeaders{
		RequestHeaders: &extProcPb.HttpHeaders{
			Headers: &envoyCorev3.HeaderMap{
				Headers: []*envoyCorev3.HeaderValue{
					{Key: ":method", RawValue: []byte("GET")},
					{Key: ":path", RawValue: []byte("/.well-known/ohttp-configs")},
					{Key: "accept", RawValue: []byte("application/ohttp-keys")},
					{Key: ":authority", RawValue: []byte("example.com")},
				},
			},
		},
	}

	go func() {
		recvChan <- &extProcPb.ProcessingRequest{
			Request: reqHeaders,
		}
		close(recvChan)
	}()

	go func() {
		err := srv.Process(stream)
		if err != nil {
			// t.Errorf("Process failed: %v", err)
		}
	}()

	resp := <-sendChan
	require.NotNil(t, resp)
	ir := resp.GetImmediateResponse()
	require.NotNil(t, ir)

	// If method check fails, it returns 405 Method Not Allowed
	// If success, it returns 200 OK
	assert.Equal(t, envoyTypePb.StatusCode_OK, ir.Status.Code, "Expected 200 OK, got %d. Body: %s", ir.Status.Code, string(ir.Body))
}

func TestProcess_IgnoredRequestTypes(t *testing.T) {
	srv := &Server{streaming: false, relayer: &mockRelayer{}, maxRequestBodySize: 1024}
	fps := &fakeProcessServer{ctx: t.Context(), recvCh: make(chan *extProcPb.ProcessingRequest, 3)}

	// Send ignored types
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_RequestTrailers{RequestTrailers: &extProcPb.HttpTrailers{}}}
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_ResponseHeaders{ResponseHeaders: &extProcPb.HttpHeaders{}}}
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: &extProcPb.ProcessingRequest_ResponseBody{ResponseBody: &extProcPb.HttpBody{}}}
	close(fps.recvCh)

	err := srv.Process(fps)
	require.NoError(t, err)
	// Should produce no responses
	assert.Empty(t, fps.sends)
}

func TestProcess_UnknownRequestType(t *testing.T) {
	srv := &Server{streaming: false, relayer: &mockRelayer{}, maxRequestBodySize: 1024}
	fps := &fakeProcessServer{ctx: t.Context(), recvCh: make(chan *extProcPb.ProcessingRequest, 1)}

	// Send nil request (unknown type)
	fps.recvCh <- &extProcPb.ProcessingRequest{Request: nil}
	close(fps.recvCh)

	err := srv.Process(fps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown request type")
}

func TestProcess_SendError(t *testing.T) {
	srv := &Server{streaming: false, relayer: &mockRelayer{}, maxRequestBodySize: 1024}
	// Mock stream that fails on Send
	stream := new(MockStream)
	stream.ctx = context.Background()

	// 1. Headers (valid)
	stream.On("Recv").Return(&extProcPb.ProcessingRequest{
		Request: &extProcPb.ProcessingRequest_RequestHeaders{
			RequestHeaders: &extProcPb.HttpHeaders{
				Headers: &envoyCorev3.HeaderMap{
					Headers: []*envoyCorev3.HeaderValue{
						{Key: "method", RawValue: []byte("POST")},
						{Key: "content-length", RawValue: []byte("0")}, // trigger error response
					},
				},
			},
		},
	}, nil).Once()

	// Expect ImmediateResponse with 411 Length Required
	stream.On("Send", mock.Anything).Return(errors.New("send failed")).Once()

	err := srv.Process(stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send response back to Envoy")
}

func TestRespondKeyConfig_Coverage(t *testing.T) {
	ks := gateway.StaticMapKeyConfigSource(map[string][]byte{})
	srv := NewServer(nil, ks, 1024, nil)

	tests := []struct {
		name         string
		method       string
		accept       string
		ksError      error
		expectedCode envoyTypePb.StatusCode
	}{
		{
			name:         "Invalid Method",
			method:       "POST",
			accept:       ContentTypeOHTTPKeys,
			expectedCode: envoyTypePb.StatusCode_MethodNotAllowed,
		},
		{
			name:         "Invalid Accept",
			method:       "GET",
			accept:       "text/plain",
			expectedCode: envoyTypePb.StatusCode_BadRequest,
		},
		{
			name:         "KeyConfig Error",
			method:       "GET",
			accept:       ContentTypeOHTTPKeys,
			ksError:      errors.New("ks error"),
			expectedCode: envoyTypePb.StatusCode_InternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ksError != nil {
				srv.keyConfigSource = gateway.KeyConfigSourceFn(func(context.Context, string) ([]byte, error) {
					return nil, tc.ksError
				})
			} else {
				srv.keyConfigSource = ks
			}

			st := &streamState{
				httpMethod: strings.ToLower(tc.method),
				host:       "example.com",
			}
			headers := &extProcPb.ProcessingRequest_RequestHeaders{
				RequestHeaders: &extProcPb.HttpHeaders{
					Headers: &envoyCorev3.HeaderMap{
						Headers: []*envoyCorev3.HeaderValue{
							{Key: "accept", RawValue: []byte(tc.accept)},
						},
					},
				},
			}

			resps, err := srv.respondKeyConfig(context.Background(), headers, logr.Discard(), st)
			if err != nil {
				// Some errors might be returned directly, but here we expect immediate responses
				t.Fatalf("unexpected error: %v", err)
			}
			require.Len(t, resps, 1)
			ir := resps[0].GetImmediateResponse()
			assert.Equal(t, tc.expectedCode, ir.Status.Code)
		})
	}
}

func TestForwardAndRespond_RelayReadError(t *testing.T) {
	// Relayer that returns a body that fails to read
	relayer := &mockRelayer{
		relayFunc: func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(&errorReader{err: errors.New("read error")}),
			}, nil
		},
	}

	srv := NewServer(relayer, nil, 1024, nil)
	st := &streamState{host: "example.com", httpMethod: "post"}

	resps, err := srv.forwardAndRespond(context.Background(), logr.Discard(), st)
	require.NoError(t, err)
	require.Len(t, resps, 1)
	ir := resps[0].GetImmediateResponse()
	assert.Equal(t, envoyTypePb.StatusCode_BadGateway, ir.Status.Code)
	// Error messages are sanitized, so we should get "Bad Gateway" instead of internal error details
	assert.Equal(t, "Bad Gateway", string(ir.Body))
}

type errorReader struct {
	err error
}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, e.err
}
