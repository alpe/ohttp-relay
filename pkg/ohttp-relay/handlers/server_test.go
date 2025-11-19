package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/relay"
	envoyCorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardAndRespond_Success(t *testing.T) {
	relayer := &mockRelayer{
		relayFunc: func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
			// Verify the parameters passed to the relayer
			assert.Equal(t, "example.test", host)
			assert.Equal(t, []byte("opaque"), body)
			assert.Equal(t, ContentTypeOHTTPReq, contentType)
			assert.Equal(t, http.MethodPost, method)

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{ContentTypeOHTTPRes}},
				Body:       io.NopCloser(bytes.NewReader([]byte("res-opaque"))),
			}, nil
		},
	}
	srv := &Server{streaming: false, relayer: relayer, maxRequestBodySize: 10 * 1024 * 1024}

	st := &streamState{
		host:           "example.test",
		reqContentType: ContentTypeOHTTPReq,
		httpMethod:     http.MethodPost,
		body:           []byte("opaque"),
	}

	resps, err := srv.forwardAndRespond(t.Context(), logr.Discard(), st)
	require.NoError(t, err)
	require.Len(t, resps, 1)

	ir := getImmediateResp(t, resps[0])
	assert.Equal(t, envoyTypePb.StatusCode_OK, ir.Status.Code)
	assert.Equal(t, []byte("res-opaque"), ir.Body)

	// content-type header to client must be message/ohttp-res
	require.NotNil(t, ir.Headers)
	var ct string
	for _, h := range ir.Headers.SetHeaders {
		if h.GetHeader().GetKey() == "content-type" {
			ct = string(h.GetHeader().GetRawValue())
			break
		}
	}
	assert.Equal(t, ContentTypeOHTTPRes, ct)
}

func getImmediateResp(t *testing.T, pr *extProcPb.ProcessingResponse) *extProcPb.ImmediateResponse {
	t.Helper()
	ir, ok := pr.Response.(*extProcPb.ProcessingResponse_ImmediateResponse)
	require.True(t, ok, "expected ImmediateResponse in ProcessingResponse")
	return ir.ImmediateResponse
}

func TestForwardAndRespond_NoMappingAndErrors(t *testing.T) {
	specs := map[string]struct {
		relayer *mockRelayer
		expMsg  string
	}{
		"no mapping": {
			relayer: &mockRelayer{
				relayFunc: func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
					return nil, relay.ErrNoGateway
				},
			},
			expMsg: "Bad Gateway",
		},
		"mapping error": {
			relayer: &mockRelayer{
				relayFunc: func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
					return nil, assert.AnError
				},
			},
			expMsg: "Bad Gateway",
		},
	}
	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			srv := &Server{streaming: false, relayer: spec.relayer, maxRequestBodySize: 10 * 1024 * 1024}
			st := &streamState{host: "x", httpMethod: http.MethodPost}
			resps, err := srv.forwardAndRespond(t.Context(), logr.Discard(), st)
			require.NoError(t, err)
			require.Len(t, resps, 1)
			ir := getImmediateResp(t, resps[0])
			assert.Equal(t, envoyTypePb.StatusCode_BadGateway, ir.Status.Code)
			assert.Contains(t, string(ir.Body), spec.expMsg)
			// error responses should be text/plain
			var ct string
			for _, h := range ir.Headers.SetHeaders {
				if h.GetHeader().GetKey() == "content-type" {
					ct = string(h.GetHeader().GetRawValue())
					break
				}
			}
			assert.Equal(t, "text/plain", ct)
		})
	}
}

func TestForwardAndRespond_GatewayNon2xx(t *testing.T) {
	relayer := &mockRelayer{
		relayFunc: func(ctx context.Context, host string, body []byte, contentType string, method string) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader([]byte("oops"))),
			}, nil
		},
	}
	srv := &Server{streaming: false, relayer: relayer, maxRequestBodySize: 10 * 1024 * 1024}
	st := &streamState{host: "x", httpMethod: http.MethodPost, body: []byte("opaque"), reqContentType: "application/json"}

	resps, err := srv.forwardAndRespond(t.Context(), logr.Discard(), st)
	require.NoError(t, err)
	require.Len(t, resps, 1)
	ir := getImmediateResp(t, resps[0])
	assert.Equal(t, envoyTypePb.StatusCode_BadGateway, ir.Status.Code)
	assert.Contains(t, string(ir.Body), "Bad Gateway")
}

func TestExtractHeaderValue(t *testing.T) {
	req := &extProcPb.ProcessingRequest_RequestHeaders{
		RequestHeaders: &extProcPb.HttpHeaders{
			Headers: &envoyCorev3.HeaderMap{
				Headers: []*envoyCorev3.HeaderValue{
					{Key: "Content-Type", RawValue: []byte("application/json")},
					{Key: ":authority", RawValue: []byte("EXAMPLE.test")},
				},
			},
		},
	}

	assert.Equal(t, "application/json", extractHeaderValue(req, "content-type"))
	assert.Equal(t, "EXAMPLE.test", extractHeaderValue(req, ":AUTHORITY"))
	assert.Equal(t, "", extractHeaderValue(req, "missing"))
}
