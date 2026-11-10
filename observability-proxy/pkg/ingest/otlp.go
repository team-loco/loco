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
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	LogsPath    = "/v1/logs"
	TracesPath  = "/v1/traces"
	MetricsPath = "/v1/metrics"

	protobufContentType = "application/x-protobuf"
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

type otlpRequest interface {
	UnmarshalProto(data []byte) error
}

type otlpResponse interface {
	MarshalProto() ([]byte, error)
}

// signal describes one OTLP signal so a single export path serves logs, traces and metrics.
type signal[Req otlpRequest, Resp otlpResponse, Payload any] struct {
	newRequest  func() Req
	newResponse func() Resp
	payload     func(Req) Payload
	filter      func(*tenancyFilter, Payload)
	count       func(Payload) int
	consume     func(context.Context, Payload) error
	reject      func(Resp, int64, string)
}

func (s *Service) logsSignal() signal[plogotlp.ExportRequest, plogotlp.ExportResponse, plog.Logs] {
	return signal[plogotlp.ExportRequest, plogotlp.ExportResponse, plog.Logs]{
		newRequest:  plogotlp.NewExportRequest,
		newResponse: plogotlp.NewExportResponse,
		payload:     plogotlp.ExportRequest.Logs,
		filter:      (*tenancyFilter).logs,
		count:       plog.Logs.LogRecordCount,
		consume:     s.consumers.Logs.ConsumeLogs,
		reject: func(response plogotlp.ExportResponse, rejected int64, message string) {
			response.PartialSuccess().SetRejectedLogRecords(rejected)
			response.PartialSuccess().SetErrorMessage(message)
		},
	}
}

func (s *Service) tracesSignal() signal[ptraceotlp.ExportRequest, ptraceotlp.ExportResponse, ptrace.Traces] {
	return signal[ptraceotlp.ExportRequest, ptraceotlp.ExportResponse, ptrace.Traces]{
		newRequest:  ptraceotlp.NewExportRequest,
		newResponse: ptraceotlp.NewExportResponse,
		payload:     ptraceotlp.ExportRequest.Traces,
		filter:      (*tenancyFilter).traces,
		count:       ptrace.Traces.SpanCount,
		consume:     s.consumers.Traces.ConsumeTraces,
		reject: func(response ptraceotlp.ExportResponse, rejected int64, message string) {
			response.PartialSuccess().SetRejectedSpans(rejected)
			response.PartialSuccess().SetErrorMessage(message)
		},
	}
}

func (s *Service) metricsSignal() signal[pmetricotlp.ExportRequest, pmetricotlp.ExportResponse, pmetric.Metrics] {
	return signal[pmetricotlp.ExportRequest, pmetricotlp.ExportResponse, pmetric.Metrics]{
		newRequest:  pmetricotlp.NewExportRequest,
		newResponse: pmetricotlp.NewExportResponse,
		payload:     pmetricotlp.ExportRequest.Metrics,
		filter:      (*tenancyFilter).metrics,
		count:       pmetric.Metrics.DataPointCount,
		consume:     s.consumers.Metrics.ConsumeMetrics,
		reject: func(response pmetricotlp.ExportResponse, rejected int64, message string) {
			response.PartialSuccess().SetRejectedDataPoints(rejected)
			response.PartialSuccess().SetErrorMessage(message)
		},
	}
}

func export[Req otlpRequest, Resp otlpResponse, Payload any](
	ctx context.Context,
	s *Service,
	sig signal[Req, Resp, Payload],
	admission *Admission,
	request Req,
) (Resp, error) {
	response := sig.newResponse()
	payload := sig.payload(request)
	filter := newTenancyFilter(ctx, admission)
	sig.filter(filter, payload)
	if filter.err != nil {
		return response, filter.err
	}
	if rejected := filter.rejected(); rejected > 0 {
		sig.reject(response, rejected, filter.message())
	}
	if sig.count(payload) > 0 {
		if err := sig.consume(ctx, payload); err != nil {
			return response, err
		}
	}
	filter.record(s.rejected)
	return response, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST "+LogsPath, exportHandler(s, s.logsSignal()))
	mux.Handle("POST "+TracesPath, exportHandler(s, s.tracesSignal()))
	mux.Handle("POST "+MetricsPath, exportHandler(s, s.metricsSignal()))
	return mux
}

func checkContentType(header string) error {
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil || mediaType != protobufContentType {
		return errUnsupportedContentType
	}
	return nil
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

func exportHandler[Req otlpRequest, Resp otlpResponse, Payload any](
	s *Service,
	sig signal[Req, Resp, Payload],
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := checkContentType(r.Header.Get("Content-Type")); err != nil {
			s.writeHTTPError(w, http.StatusUnsupportedMediaType, codes.InvalidArgument, err)
			return
		}
		admission, err := s.gate.Admit(r.Context(), r.Header.Get(authorizationHeader))
		if err != nil {
			s.writeFailure(r.Context(), w, err)
			return
		}
		body, err := s.readBody(r)
		if err != nil {
			s.writeFailure(r.Context(), w, err)
			return
		}
		request := sig.newRequest()
		if decodeErr := request.UnmarshalProto(body); decodeErr != nil {
			decodeFailure := consumererror.NewPermanent(fmt.Errorf("decode request: %w", decodeErr))
			s.writeFailure(r.Context(), w, decodeFailure)
			return
		}
		response, err := export(r.Context(), s, sig, admission, request)
		if err != nil {
			s.writeFailure(r.Context(), w, err)
			return
		}
		payload, err := response.MarshalProto()
		if err != nil {
			s.writeFailure(r.Context(), w, fmt.Errorf("encode response: %w", err))
			return
		}
		w.Header().Set("Content-Type", protobufContentType)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(payload); err != nil { //nolint:gosec // G705: an OTLP protobuf body, never HTML
			slog.WarnContext(r.Context(), "write OTLP response", "error", err)
		}
	}
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

// logExportFailure reports ERROR unless the sentinel shows the client is at fault. A permanent
// consumer error stays ERROR: decoding and exporter failures both produce one, and the type
// cannot tell a bad payload from a rejected insert.
func logExportFailure(ctx context.Context, err, public error, attrs ...any) {
	level := slog.LevelError
	switch {
	case errors.Is(public, ErrUnauthenticated):
		level = slog.LevelDebug
	case errors.Is(public, ErrClusterRevoked):
		level = slog.LevelDebug
	case errors.Is(public, ErrTokenExpired):
		level = slog.LevelDebug
	case errors.Is(public, errRequestTooLarge):
		level = slog.LevelDebug
	}
	slog.Log(ctx, level, "OTLP export failed", append([]any{"error", err}, attrs...)...)
}

func (s *Service) writeFailure(ctx context.Context, w http.ResponseWriter, err error) {
	httpStatus, code, public := publicError(err)
	logExportFailure(ctx, err, public, "status", httpStatus)
	s.writeHTTPError(w, httpStatus, code, public)
}

func (s *Service) writeHTTPError(w http.ResponseWriter, httpStatus int, code codes.Code, public error) {
	payload, err := proto.Marshal(s.status(code, public).Proto())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", protobufContentType)
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
	logExportFailure(ctx, err, public, "code", code.String())
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

// grpcExport serves one signal over gRPC. The per-signal servers stay because each must embed
// its own UnimplementedGRPCServer.
func grpcExport[Req otlpRequest, Resp otlpResponse, Payload any](
	s *Service,
	sig signal[Req, Resp, Payload],
) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, request Req) (Resp, error) {
		admission, err := s.admitGRPC(ctx)
		if err != nil {
			return sig.newResponse(), s.grpcError(ctx, err)
		}
		response, err := export(ctx, s, sig, admission, request)
		if err != nil {
			return response, s.grpcError(ctx, err)
		}
		return response, nil
	}
}

type logsServer struct {
	plogotlp.UnimplementedGRPCServer
	export func(context.Context, plogotlp.ExportRequest) (plogotlp.ExportResponse, error)
}

func (l *logsServer) Export(ctx context.Context, request plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	return l.export(ctx, request)
}

type tracesServer struct {
	ptraceotlp.UnimplementedGRPCServer
	export func(context.Context, ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error)
}

func (t *tracesServer) Export(
	ctx context.Context,
	request ptraceotlp.ExportRequest,
) (ptraceotlp.ExportResponse, error) {
	return t.export(ctx, request)
}

type metricsServer struct {
	pmetricotlp.UnimplementedGRPCServer
	export func(context.Context, pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error)
}

func (m *metricsServer) Export(
	ctx context.Context,
	request pmetricotlp.ExportRequest,
) (pmetricotlp.ExportResponse, error) {
	return m.export(ctx, request)
}

func (s *Service) GRPCServer() *grpc.Server {
	server := grpc.NewServer(grpc.MaxRecvMsgSize(s.maxRequestBytes))
	plogotlp.RegisterGRPCServer(server, &logsServer{export: grpcExport(s, s.logsSignal())})
	ptraceotlp.RegisterGRPCServer(server, &tracesServer{export: grpcExport(s, s.tracesSignal())})
	pmetricotlp.RegisterGRPCServer(server, &metricsServer{export: grpcExport(s, s.metricsSignal())})
	return server
}
