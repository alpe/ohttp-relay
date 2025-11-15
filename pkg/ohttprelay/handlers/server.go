package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alpe/ohttprelay/internal/ctrl"
	envoyCorev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	envoyTypePb "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/alpe/ohttprelay/pkg/ohttprelay/metrics"
	"github.com/alpe/ohttprelay/pkg/ohttprelay/relay"
	logutil "github.com/alpe/ohttprelay/pkg/ohttprelay/util/logging"
)

// Media types per RFC 9458 / OHTTP
const (
	ContentTypeOHTTPReq = "message/ohttp-req"
	ContentTypeOHTTPRes = "message/ohttp-res"
)

// Source moved to package gateway.

// Server is a minimal ExternalProcessor that acts as an OHTTP Relay.
// It forwards opaque OHTTP request bodies to a configured gateway and
// returns the opaque response back to Envoy. No decryption takes place here.
// This is a buffered implementation: we wait for EndOfStream before forwarding.
type Server struct {
	streaming          bool
	relayer            relay.Relayer
	maxRequestBodySize int64
}

// NewServer constructs a new OHTTP relay handler.
func NewServer(relayer relay.Relayer, maxRequestBodySize int64) *Server {
	return &Server{
		streaming:          false,
		relayer:            relayer,
		maxRequestBodySize: maxRequestBodySize,
	}
}

func (s *Server) Process(srv extProcPb.ExternalProcessor_ProcessServer) error {
	ctx := srv.Context()
	logger := ctrl.FromContext(ctx)
	loggerVerbose := logger.V(logutil.VERBOSE)
	loggerVerbose.Info("OHTTP Relay: Processing stream")

	metrics.ActiveRequests.Add(ctx, 1)
	defer metrics.ActiveRequests.Add(ctx, -1)

	start := time.Now()
	st := &streamState{}
	var reqSize, respSize int64
	var statusCode int = 200 // Default to 200 if not set (though we usually set it)

	// Defer recording of final metrics
	defer func() {
		duration := time.Since(start)
		statusCategory := fmt.Sprintf("%dxx", statusCode/100)
		// If method is empty, it might be an error before headers were processed
		method := st.httpMethod
		if method == "" {
			method = "UNKNOWN"
		}
		metrics.RecordRequest(ctx, method, statusCategory, duration, reqSize, respSize)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, recvErr := srv.Recv()
		if recvErr == io.EOF || errors.Is(recvErr, context.Canceled) {
			return nil
		}
		if recvErr != nil {
			metrics.RecordError(ctx, "recv_error")
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", recvErr)
		}
		logger.Info("processing ", "type", fmt.Sprintf("%T", req.Request))
		var responses []*extProcPb.ProcessingResponse
		var err error
		switch v := req.Request.(type) {
		case *extProcPb.ProcessingRequest_RequestHeaders:
			// capture host and validate content-type if present
			if requestId := extractHeaderValue(v, RequestIdHeaderKey); len(requestId) > 0 {
				logger = logger.WithValues(RequestIdHeaderKey, requestId)
			}
			st.httpMethod = strings.ToLower(extractHeaderValue(v, "method"))

			st.host = strings.ToLower(extractHeaderValue(v, ":authority"))
			if st.host == "" {
				st.host = strings.ToLower(extractHeaderValue(v, "host"))
			}
			st.reqContentType = extractHeaderValue(v, "content-type")
			// no response for headers
		case *extProcPb.ProcessingRequest_RequestBody:
			if logger.V(logutil.DEBUG).Enabled() {
				logger.V(logutil.DEBUG).Info("Incoming body chunk", "len", len(v.RequestBody.Body), "EoS", v.RequestBody.EndOfStream)
			}
			bodyLen := int64(len(v.RequestBody.Body))
			reqSize += bodyLen
			if int64(len(st.body))+bodyLen > s.maxRequestBodySize {
				responses = immediateErrorResponse(logger, http.StatusRequestEntityTooLarge, "request body too large")
				// Clear body to free memory
				st.body = nil
				// We must return immediately
				goto SendResponses
			}
			st.body = append(st.body, v.RequestBody.Body...)
			if v.RequestBody.EndOfStream {
				responses, err = s.forwardAndRespond(ctx, logger, st)
			}
		case *extProcPb.ProcessingRequest_RequestTrailers:
			// ignore
		case *extProcPb.ProcessingRequest_ResponseHeaders,
			*extProcPb.ProcessingRequest_ResponseBody:
			// we do request-side only
		default:
			logger.V(logutil.DEFAULT).Error(nil, "Unknown Request type", "request", v)
			metrics.RecordError(ctx, "unknown_request_type")
			return status.Error(codes.Unknown, "unknown request type")
		}
		if err != nil {
			if logger.V(logutil.DEBUG).Enabled() {
				logger.V(logutil.DEBUG).Error(err, "Failed to process request", "request", req)
			} else {
				logger.V(logutil.DEFAULT).Error(err, "Failed to process request")
			}
			metrics.RecordError(ctx, "process_error")
			return status.Errorf(status.Code(err), "failed to handle request: %v", err)
		}

	SendResponses:
		for _, resp := range responses {
			loggerVerbose.Info("Response generated")

			// Extract status code and size from response
			if ir := resp.GetImmediateResponse(); ir != nil {
				if ir.Status != nil {
					statusCode = int(ir.Status.Code)
				}
				respSize += int64(len(ir.Body))
			}

			if err := srv.Send(resp); err != nil {
				logger.V(logutil.DEFAULT).Error(err, "Send failed")
				metrics.RecordError(ctx, "send_error")
				return status.Errorf(codes.Unknown, "failed to send response back to Envoy: %v", err)
			}
		}
	}
}

type streamState struct {
	host           string
	reqContentType string
	httpMethod     string
	body           []byte
}

func (s *Server) forwardAndRespond(ctx context.Context, logger logr.Logger, st *streamState) ([]*extProcPb.ProcessingResponse, error) {
	logger.V(logutil.DEFAULT).Info("Forwarding request to gateway")

	// Ensure content type is OHTTP req, but allow missing
	contentType := ContentTypeOHTTPReq
	if st.reqContentType != "" && !strings.EqualFold(st.reqContentType, ContentTypeOHTTPReq) {
		logger.V(logutil.DEFAULT).Info("Unexpected content-type; proceeding anyway", "content-type", st.reqContentType)
		contentType = st.reqContentType
	}

	resp, err := s.relayer.Relay(ctx, st.host, st.body, contentType, st.httpMethod)
	if err != nil {
		// Check if it's a "no gateway mapping" error or other relay error
		if errors.Is(err, relay.ErrNoGateway) {
			return immediateErrorResponse(logger, http.StatusBadGateway, err.Error()), nil
		}
		return immediateErrorResponse(logger, http.StatusBadGateway, fmt.Sprintf("relay request failed: %v", err)), nil
	}

	defer resp.Body.Close()
	respBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return immediateErrorResponse(logger, http.StatusBadGateway, fmt.Sprintf("failed reading relay response: %v", readErr)), nil
	}

	if resp.StatusCode/100 != 2 {
		return immediateErrorResponse(logger, http.StatusBadGateway, fmt.Sprintf("gateway status %d", resp.StatusCode)), nil
	}

	// Build ImmediateResponse to Envoy with OHTTP response bytes.
	// We do not forward any headers from the gateway response; we only set
	// our own content-type for the opaque OHTTP payload.
	ir := &extProcPb.ImmediateResponse{
		Status: &envoyTypePb.HttpStatus{Code: envoyTypePb.StatusCode_OK},
		Headers: &extProcPb.HeaderMutation{
			SetHeaders: []*envoyCorev3.HeaderValueOption{
				{Header: &envoyCorev3.HeaderValue{Key: "content-type", RawValue: []byte(ContentTypeOHTTPRes)}},
			},
		},
		Body: respBytes,
	}

	return []*extProcPb.ProcessingResponse{{Response: &extProcPb.ProcessingResponse_ImmediateResponse{ImmediateResponse: ir}}}, nil
}

func immediateErrorResponse(logger logr.Logger, code int, internalMsg string) []*extProcPb.ProcessingResponse {
	// Log the internal details
	logger.V(logutil.DEFAULT).Info("Sending error response", "code", code, "details", internalMsg)

	statusCode := envoyTypePb.StatusCode_InternalServerError
	switch code {
	case http.StatusBadRequest:
		statusCode = envoyTypePb.StatusCode_BadRequest
	case http.StatusUnauthorized:
		statusCode = envoyTypePb.StatusCode_Unauthorized
	case http.StatusForbidden:
		statusCode = envoyTypePb.StatusCode_Forbidden
	case http.StatusNotFound:
		statusCode = envoyTypePb.StatusCode_NotFound
	case http.StatusRequestEntityTooLarge:
		statusCode = envoyTypePb.StatusCode_PayloadTooLarge
	case http.StatusTooManyRequests:
		statusCode = envoyTypePb.StatusCode_TooManyRequests
	case http.StatusBadGateway:
		statusCode = envoyTypePb.StatusCode_BadGateway
	case http.StatusServiceUnavailable:
		statusCode = envoyTypePb.StatusCode_ServiceUnavailable
	case http.StatusGatewayTimeout:
		statusCode = envoyTypePb.StatusCode_GatewayTimeout
	}

	// Sanitize the message sent to the client
	clientMsg := http.StatusText(code)
	if clientMsg == "" {
		clientMsg = "Error"
	}

	ir := &extProcPb.ImmediateResponse{
		Status: &envoyTypePb.HttpStatus{Code: statusCode},
		Headers: &extProcPb.HeaderMutation{
			SetHeaders: []*envoyCorev3.HeaderValueOption{
				{Header: &envoyCorev3.HeaderValue{Key: "content-type", RawValue: []byte("text/plain")}},
			},
		},
		Body: []byte(clientMsg),
	}
	return []*extProcPb.ProcessingResponse{{Response: &extProcPb.ProcessingResponse_ImmediateResponse{ImmediateResponse: ir}}}
}

const (
	RequestIdHeaderKey = "x-request-id"
)

func extractHeaderValue(req *extProcPb.ProcessingRequest_RequestHeaders, headerKey string) string {
	// header key should be case insensitive
	headerKeyInLower := strings.ToLower(headerKey)
	if req != nil && req.RequestHeaders != nil && req.RequestHeaders.Headers != nil {
		for _, headerKv := range req.RequestHeaders.Headers.Headers {
			if strings.ToLower(headerKv.Key) == headerKeyInLower {
				return string(headerKv.RawValue)
			}
		}
	}
	return ""
}
