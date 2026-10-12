package interceptor

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
)

var errPrivateFailure = errors.New("private handler diagnostic")

type failingConfig struct {
	configv1connect.UnimplementedConfigServiceHandler
}

func (*failingConfig) GetConfig(context.Context, *configv1.GetConfigRequest) (*configv1.GetConfigResponse, error) {
	return nil, errPrivateFailure
}

func TestPlainHandlerFailureGetsPublicWireMessage(t *testing.T) {
	server := connect.NewServer(NewErrorInterceptor())
	configv1connect.RegisterConfigServiceHandler(server, &failingConfig{})
	transport := connectinprocess.New(server)
	client := configv1connect.NewConfigServiceClient(connect.NewClient(transport))
	_, err := client.GetConfig(t.Context(), &configv1.GetConfigRequest{})
	failure, ok := errors.AsType[*connect.Error](err)
	if !ok || failure.Code() != connect.CodeInternal || failure.Message() != publicRequestFailed {
		t.Fatalf("wire error = %v", err)
	}
}

func TestPublicHandlerErrorsKeepServerCause(t *testing.T) {
	for _, cause := range []error{errPrivateFailure, context.Canceled, context.DeadlineExceeded} {
		wrapped := NewErrorInterceptor()(
			func(context.Context, connect.Spec, connect.ServerStream) error { return cause },
		)
		err := wrapped(t.Context(), connect.Spec{}, nil)
		if !errors.Is(err, cause) {
			t.Fatalf("error = %v, cause = %v", err, cause)
		}
	}
}
