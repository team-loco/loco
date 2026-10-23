package ingest

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	LogsPath    = "/v1/logs"
	TracesPath  = "/v1/traces"
	MetricsPath = "/v1/metrics"

	protobufContentType = "application/x-protobuf"
	jsonContentType     = "application/json"
	gzipEncoding        = "gzip"
)

var (
	errUnsupportedContentType = errors.New("unsupported content type")
	errUnsupportedEncoding    = errors.New("unsupported content encoding")
	errRequestTooLarge        = errors.New("request body too large")
	errInvalidRequest         = errors.New("invalid OTLP request")
	errStorageUnavailable     = errors.New("telemetry storage unavailable")
)

type Consumers struct {
	Logs    consumer.Logs
	Traces  consumer.Traces
	Metrics consumer.Metrics
}

type Service struct {
	consumers       Consumers
	maxRequestBytes int
}

func NewService(consumers Consumers, maxRequestBytes int) *Service {
	return &Service{consumers: consumers, maxRequestBytes: maxRequestBytes}
}

type encoding int

const (
	protobufEncoding encoding = iota
	jsonEncoding
)

func (e encoding) contentType() string {
	if e == jsonEncoding {
		return jsonContentType
	}
	return protobufContentType
}

func (e encoding) marshal(message proto.Message) ([]byte, error) {
	if e == jsonEncoding {
		return protojson.Marshal(message)
	}
	return proto.Marshal(message)
}

type otlpRequest interface {
	UnmarshalProto(data []byte) error
	UnmarshalJSON(data []byte) error
}

type otlpResponse interface {
	MarshalProto() ([]byte, error)
	MarshalJSON() ([]byte, error)
}

func (s *Service) exportLogs(ctx context.Context, request plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	response := plogotlp.NewExportResponse()
	return response, s.consumers.Logs.ConsumeLogs(ctx, request.Logs())
}

func (s *Service) exportTraces(
	ctx context.Context,
	request ptraceotlp.ExportRequest,
) (ptraceotlp.ExportResponse, error) {
	response := ptraceotlp.NewExportResponse()
	return response, s.consumers.Traces.ConsumeTraces(ctx, request.Traces())
}

func (s *Service) exportMetrics(
	ctx context.Context,
	request pmetricotlp.ExportRequest,
) (pmetricotlp.ExportResponse, error) {
	response := pmetricotlp.NewExportResponse()
	return response, s.consumers.Metrics.ConsumeMetrics(ctx, request.Metrics())
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST "+LogsPath, exportHandler(s, plogotlp.NewExportRequest, s.exportLogs))
	mux.Handle("POST "+TracesPath, exportHandler(s, ptraceotlp.NewExportRequest, s.exportTraces))
	mux.Handle("POST "+MetricsPath, exportHandler(s, pmetricotlp.NewExportRequest, s.exportMetrics))
	return mux
}

func requestEncoding(header string) (encoding, error) {
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return protobufEncoding, errUnsupportedContentType
	}
	switch mediaType {
	case protobufContentType:
		return protobufEncoding, nil
	case jsonContentType:
		return jsonEncoding, nil
	default:
		return protobufEncoding, errUnsupportedContentType
	}
}

func (s *Service) readBody(r *http.Request) ([]byte, error) {
	var reader io.Reader
	switch r.Header.Get("Content-Encoding") {
	case "":
		reader = r.Body
	case gzipEncoding:
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, consumererror.NewPermanent(fmt.Errorf("open gzip body: %w", err))
		}
		defer gz.Close()
		reader = gz
	default:
		return nil, consumererror.NewPermanent(errUnsupportedEncoding)
	}
	limit := int64(s.maxRequestBytes) + 1
	body, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		return nil, consumererror.NewPermanent(fmt.Errorf("read body: %w", err))
	}
	if len(body) > s.maxRequestBytes {
		return nil, errRequestTooLarge
	}
	return body, nil
}

func exportHandler[Req otlpRequest, Resp otlpResponse](
	s *Service,
	newRequest func() Req,
	export func(context.Context, Req) (Resp, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		enc, err := requestEncoding(r.Header.Get("Content-Type"))
		if err != nil {
			writeHTTPError(w, enc, http.StatusUnsupportedMediaType, codes.InvalidArgument, err)
			return
		}
		body, err := s.readBody(r)
		if err != nil {
			writeFailure(r.Context(), w, enc, err)
			return
		}
		request := newRequest()
		if decodeErr := unmarshal(enc, request, body); decodeErr != nil {
			decodeFailure := consumererror.NewPermanent(fmt.Errorf("decode request: %w", decodeErr))
			writeFailure(r.Context(), w, enc, decodeFailure)
			return
		}
		response, err := export(r.Context(), request)
		if err != nil {
			writeFailure(r.Context(), w, enc, err)
			return
		}
		payload, err := marshal(enc, response)
		if err != nil {
			writeFailure(r.Context(), w, enc, fmt.Errorf("encode response: %w", err))
			return
		}
		w.Header().Set("Content-Type", enc.contentType())
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(payload); err != nil {
			slog.WarnContext(r.Context(), "write OTLP response", "error", err)
		}
	}
}

func unmarshal(enc encoding, request otlpRequest, body []byte) error {
	if enc == jsonEncoding {
		return request.UnmarshalJSON(body)
	}
	return request.UnmarshalProto(body)
}

func marshal(enc encoding, response otlpResponse) ([]byte, error) {
	if enc == jsonEncoding {
		return response.MarshalJSON()
	}
	return response.MarshalProto()
}

func publicError(err error) (int, codes.Code, error) {
	switch {
	case errors.Is(err, errRequestTooLarge):
		return http.StatusRequestEntityTooLarge, codes.InvalidArgument, errRequestTooLarge
	case consumererror.IsPermanent(err):
		return http.StatusBadRequest, codes.InvalidArgument, errInvalidRequest
	default:
		return http.StatusServiceUnavailable, codes.Unavailable, errStorageUnavailable
	}
}

func writeFailure(ctx context.Context, w http.ResponseWriter, enc encoding, err error) {
	httpStatus, code, public := publicError(err)
	slog.ErrorContext(ctx, "OTLP export failed", "error", err, "status", httpStatus)
	writeHTTPError(w, enc, httpStatus, code, public)
}

func writeHTTPError(w http.ResponseWriter, enc encoding, httpStatus int, code codes.Code, public error) {
	statusProto := status.New(code, public.Error()).Proto()
	payload, err := enc.marshal(statusProto)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", enc.contentType())
	w.WriteHeader(httpStatus)
	if _, err := w.Write(payload); err != nil {
		slog.Warn("write OTLP error response", "error", err)
	}
}

func grpcError(ctx context.Context, err error) error {
	_, code, public := publicError(err)
	slog.ErrorContext(ctx, "OTLP export failed", "error", err, "code", code.String())
	return status.Error(code, public.Error())
}

type logsServer struct {
	plogotlp.UnimplementedGRPCServer
	service *Service
}

func (l *logsServer) Export(ctx context.Context, request plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	response, err := l.service.exportLogs(ctx, request)
	if err != nil {
		return response, grpcError(ctx, err)
	}
	return response, nil
}

type tracesServer struct {
	ptraceotlp.UnimplementedGRPCServer
	service *Service
}

func (t *tracesServer) Export(
	ctx context.Context,
	request ptraceotlp.ExportRequest,
) (ptraceotlp.ExportResponse, error) {
	response, err := t.service.exportTraces(ctx, request)
	if err != nil {
		return response, grpcError(ctx, err)
	}
	return response, nil
}

type metricsServer struct {
	pmetricotlp.UnimplementedGRPCServer
	service *Service
}

func (m *metricsServer) Export(
	ctx context.Context,
	request pmetricotlp.ExportRequest,
) (pmetricotlp.ExportResponse, error) {
	response, err := m.service.exportMetrics(ctx, request)
	if err != nil {
		return response, grpcError(ctx, err)
	}
	return response, nil
}

func (s *Service) GRPCServer() *grpc.Server {
	server := grpc.NewServer(grpc.MaxRecvMsgSize(s.maxRequestBytes))
	plogotlp.RegisterGRPCServer(server, &logsServer{service: s})
	ptraceotlp.RegisterGRPCServer(server, &tracesServer{service: s})
	pmetricotlp.RegisterGRPCServer(server, &metricsServer{service: s})
	return server
}
