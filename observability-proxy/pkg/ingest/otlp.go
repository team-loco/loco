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
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	LogsPath    = "/v1/logs"
	TracesPath  = "/v1/traces"
	MetricsPath = "/v1/metrics"

	protobufContentType = "application/x-protobuf"
	jsonContentType     = "application/json"
	gzipEncoding        = "gzip"

	authorizationHeader   = "Authorization"
	authorizationMetadata = "authorization"
	retryAfterHeader      = "Retry-After"
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
	gate            *Gate
	retryAfter      time.Duration
	rejected        *prometheus.CounterVec
}

type ServiceConfig struct {
	MaxRequestBytes int
	RetryAfter      time.Duration
}

func NewService(consumers Consumers, gate *Gate, cfg ServiceConfig, registerer prometheus.Registerer) *Service {
	return &Service{
		consumers:       consumers,
		maxRequestBytes: cfg.MaxRequestBytes,
		gate:            gate,
		retryAfter:      cfg.RetryAfter,
		rejected:        newRejectedRecords(registerer),
	}
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

func (s *Service) exportLogs(
	ctx context.Context,
	admission *Admission,
	request plogotlp.ExportRequest,
) (plogotlp.ExportResponse, error) {
	response := plogotlp.NewExportResponse()
	logs := request.Logs()
	filter := newTenancyFilter(ctx, admission)
	filter.logs(logs)
	if filter.err != nil {
		return response, filter.err
	}
	if rejected := filter.rejected(); rejected > 0 {
		response.PartialSuccess().SetRejectedLogRecords(rejected)
		response.PartialSuccess().SetErrorMessage(filter.message())
	}
	if logs.LogRecordCount() > 0 {
		if err := s.consumers.Logs.ConsumeLogs(ctx, logs); err != nil {
			return response, err
		}
	}
	filter.record(s.rejected)
	return response, nil
}

func (s *Service) exportTraces(
	ctx context.Context,
	admission *Admission,
	request ptraceotlp.ExportRequest,
) (ptraceotlp.ExportResponse, error) {
	response := ptraceotlp.NewExportResponse()
	traces := request.Traces()
	filter := newTenancyFilter(ctx, admission)
	filter.traces(traces)
	if filter.err != nil {
		return response, filter.err
	}
	if rejected := filter.rejected(); rejected > 0 {
		response.PartialSuccess().SetRejectedSpans(rejected)
		response.PartialSuccess().SetErrorMessage(filter.message())
	}
	if traces.SpanCount() > 0 {
		if err := s.consumers.Traces.ConsumeTraces(ctx, traces); err != nil {
			return response, err
		}
	}
	filter.record(s.rejected)
	return response, nil
}

func (s *Service) exportMetrics(
	ctx context.Context,
	admission *Admission,
	request pmetricotlp.ExportRequest,
) (pmetricotlp.ExportResponse, error) {
	response := pmetricotlp.NewExportResponse()
	metrics := request.Metrics()
	filter := newTenancyFilter(ctx, admission)
	filter.metrics(metrics)
	if filter.err != nil {
		return response, filter.err
	}
	if rejected := filter.rejected(); rejected > 0 {
		response.PartialSuccess().SetRejectedDataPoints(rejected)
		response.PartialSuccess().SetErrorMessage(filter.message())
	}
	if metrics.DataPointCount() > 0 {
		if err := s.consumers.Metrics.ConsumeMetrics(ctx, metrics); err != nil {
			return response, err
		}
	}
	filter.record(s.rejected)
	return response, nil
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
	export func(context.Context, *Admission, Req) (Resp, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		enc, err := requestEncoding(r.Header.Get("Content-Type"))
		if err != nil {
			s.writeHTTPError(w, enc, http.StatusUnsupportedMediaType, codes.InvalidArgument, err)
			return
		}
		admission, err := s.gate.Admit(r.Context(), r.Header.Get(authorizationHeader))
		if err != nil {
			s.writeFailure(r.Context(), w, enc, err)
			return
		}
		body, err := s.readBody(r)
		if err != nil {
			s.writeFailure(r.Context(), w, enc, err)
			return
		}
		request := newRequest()
		if decodeErr := unmarshal(enc, request, body); decodeErr != nil {
			decodeFailure := consumererror.NewPermanent(fmt.Errorf("decode request: %w", decodeErr))
			s.writeFailure(r.Context(), w, enc, decodeFailure)
			return
		}
		response, err := export(r.Context(), admission, request)
		if err != nil {
			s.writeFailure(r.Context(), w, enc, err)
			return
		}
		payload, err := marshal(enc, response)
		if err != nil {
			s.writeFailure(r.Context(), w, enc, fmt.Errorf("encode response: %w", err))
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
	case errors.Is(err, ErrUnauthenticated):
		return http.StatusUnauthorized, codes.Unauthenticated, ErrUnauthenticated
	case errors.Is(err, ErrClusterRevoked):
		return http.StatusForbidden, codes.PermissionDenied, ErrClusterRevoked
	case errors.Is(err, ErrTokenExpired):
		return http.StatusServiceUnavailable, codes.Unavailable, ErrTokenExpired
	case errors.Is(err, ErrAuthUnavailable):
		return http.StatusServiceUnavailable, codes.Unavailable, ErrAuthUnavailable
	case errors.Is(err, errRequestTooLarge):
		return http.StatusRequestEntityTooLarge, codes.InvalidArgument, errRequestTooLarge
	case consumererror.IsPermanent(err):
		return http.StatusBadRequest, codes.InvalidArgument, errInvalidRequest
	default:
		return http.StatusServiceUnavailable, codes.Unavailable, errStorageUnavailable
	}
}

func (s *Service) status(code codes.Code, public error) *status.Status {
	failure := status.New(code, public.Error())
	if code != codes.Unavailable {
		return failure
	}
	retryDelay := durationpb.New(s.retryAfter)
	withRetry, err := failure.WithDetails(&errdetails.RetryInfo{RetryDelay: retryDelay})
	if err != nil {
		return failure
	}
	return withRetry
}

func (s *Service) writeFailure(ctx context.Context, w http.ResponseWriter, enc encoding, err error) {
	httpStatus, code, public := publicError(err)
	slog.ErrorContext(ctx, "OTLP export failed", "error", err, "status", httpStatus)
	s.writeHTTPError(w, enc, httpStatus, code, public)
}

func (s *Service) writeHTTPError(w http.ResponseWriter, enc encoding, httpStatus int, code codes.Code, public error) {
	statusProto := s.status(code, public).Proto()
	payload, err := enc.marshal(statusProto)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", enc.contentType())
	if httpStatus == http.StatusServiceUnavailable {
		retryAfter := strconv.Itoa(int(s.retryAfter.Seconds()))
		w.Header().Set(retryAfterHeader, retryAfter)
	}
	w.WriteHeader(httpStatus)
	if _, err := w.Write(payload); err != nil {
		slog.Warn("write OTLP error response", "error", err)
	}
}

func (s *Service) grpcError(ctx context.Context, err error) error {
	_, code, public := publicError(err)
	slog.ErrorContext(ctx, "OTLP export failed", "error", err, "code", code.String())
	return s.status(code, public).Err()
}

func (s *Service) admitGRPC(ctx context.Context) (*Admission, error) {
	var authorization string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get(authorizationMetadata); len(values) > 0 {
			authorization = values[0]
		}
	}
	return s.gate.Admit(ctx, authorization)
}

type logsServer struct {
	plogotlp.UnimplementedGRPCServer
	service *Service
}

func (l *logsServer) Export(ctx context.Context, request plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	admission, err := l.service.admitGRPC(ctx)
	if err != nil {
		return plogotlp.NewExportResponse(), l.service.grpcError(ctx, err)
	}
	response, err := l.service.exportLogs(ctx, admission, request)
	if err != nil {
		return response, l.service.grpcError(ctx, err)
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
	admission, err := t.service.admitGRPC(ctx)
	if err != nil {
		return ptraceotlp.NewExportResponse(), t.service.grpcError(ctx, err)
	}
	response, err := t.service.exportTraces(ctx, admission, request)
	if err != nil {
		return response, t.service.grpcError(ctx, err)
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
	admission, err := m.service.admitGRPC(ctx)
	if err != nil {
		return pmetricotlp.NewExportResponse(), m.service.grpcError(ctx, err)
	}
	response, err := m.service.exportMetrics(ctx, admission, request)
	if err != nil {
		return response, m.service.grpcError(ctx, err)
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
