// Command api-example redeploys an existing service and follows its deployment over Connect RPC.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpc"
)

const reconnectDelay = time.Second

var errExampleInput = errors.New("provide -api, -token-file, -workspace and -resource")
var errNoDeployment = errors.New("the service has no deployment in this environment")
var errDeploymentStopped = errors.New("deployment was canceled or superseded")

func main() {
	api := flag.String("api", "", "API URL")
	tokenFile := flag.String("token-file", "", "file containing the token from loco token show --raw")
	workspace := flag.String("workspace", "", "workspace ID")
	resource := flag.String("resource", "", "service slug")
	environment := flag.String("environment", "production", "environment slug")
	timeout := flag.Duration("timeout", 10*time.Minute, "maximum time to follow the deployment")
	flag.Parse()
	if err := run(*api, *tokenFile, *workspace, *resource, *environment, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(api, tokenFile, workspace, slug, environment string, timeout time.Duration) error {
	if api == "" || tokenFile == "" {
		return errExampleInput
	}
	if workspace == "" || slug == "" {
		return errExampleInput
	}
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("read token file: %w", err)
	}
	interrupt, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupt, timeout)
	defer cancel()
	clients := rpc.ForHost(api, strings.TrimSpace(string(token)))
	resource, err := clients.Resource.GetResource(ctx, &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_SlugKey{
			SlugKey: &resourcev1.GetResourceSlugKey{WorkspaceId: workspace, Slug: slug},
		},
	})
	if err != nil {
		return fmt.Errorf("find service: %w", err)
	}
	environments, err := clients.Environment.ListEnvironments(
		ctx,
		&environmentv1.ListEnvironmentsRequest{WorkspaceId: workspace},
	)
	if err != nil {
		return fmt.Errorf("list environments: %w", err)
	}
	environmentID := ""
	for _, candidate := range environments.GetEnvironments() {
		if candidate.GetSlug() == environment {
			environmentID = candidate.GetId()
			break
		}
	}
	if environmentID == "" {
		return fmt.Errorf("environment %q not found", environment)
	}
	history, err := clients.Deployment.ListDeployments(ctx, &deploymentv1.ListDeploymentsRequest{
		ResourceId: resource.GetResource().GetId(), EnvironmentId: environmentID, PageSize: 1,
	})
	if err != nil {
		return fmt.Errorf("read deployment history: %w", err)
	}
	if len(history.GetDeployments()) == 0 {
		return errNoDeployment
	}
	previous, err := clients.Deployment.GetDeployment(ctx, &deploymentv1.GetDeploymentRequest{
		DeploymentId: history.GetDeployments()[0].GetId(),
	})
	if err != nil {
		return fmt.Errorf("read full deployment: %w", err)
	}
	created, err := clients.Deployment.CreateDeployment(ctx, &deploymentv1.CreateDeploymentRequest{
		RequestId:  uuid.NewString(),
		ResourceId: resource.GetResource().GetId(), EnvironmentId: environmentID,
		Spec:               previous.GetDeployment().GetSpec(),
		Trigger:            deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_REDEPLOY,
		SourceDeploymentId: previous.GetDeployment().GetId(),
	})
	if err != nil {
		return fmt.Errorf("start deployment: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "Following deployment", created.GetDeploymentId()); err != nil {
		return err
	}
	return follow(ctx, clients.Deployment, created.GetDeploymentId(), os.Stdout)
}

// Follow only retries transient watch failures. It never repeats the deployment creation.
func follow(
	ctx context.Context,
	client deploymentv1connect.DeploymentServiceClient,
	id string,
	output io.Writer,
) error {
	var after int64
	for {
		stream, err := client.WatchDeployment(
			ctx,
			&deploymentv1.WatchDeploymentRequest{DeploymentId: id, AfterSeq: after},
		)
		if err == nil {
			err = receive(stream, output, &after)
			if err == nil {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !reconnectable(err) {
			return fmt.Errorf("follow deployment: %w", err)
		}
		if _, err := fmt.Fprintf(output, "Reconnecting after activity %d\n", after); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnectDelay):
		}
	}
}

func receive(
	stream deploymentv1connect.DeploymentServiceWatchDeploymentClientStream,
	output io.Writer,
	after *int64,
) (err error) {
	defer func() { err = errors.Join(err, stream.Close()) }()
	for {
		event, err := stream.Receive()
		if err != nil {
			return err
		}
		for _, entry := range event.GetActivity() {
			if _, err := fmt.Fprintf(
				output,
				"%d %s %s\n",
				entry.GetSeq(),
				entry.GetRegion(),
				entry.GetMessage(),
			); err != nil {
				return err
			}
			*after = entry.GetSeq()
		}
		if event.GetHasMoreActivity() {
			continue
		}
		deployment := event.GetDeployment()
		if _, err := fmt.Fprintln(
			output,
			strings.TrimPrefix(deployment.GetStatus().String(), "DEPLOYMENT_STATUS_"),
		); err != nil {
			return err
		}
		switch deployment.GetStatus() {
		case deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING:
			if deployment.GetStartedAt() != nil {
				return nil
			}
		case deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED,
			deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED:
			return errDeploymentStopped
		}
	}
}

func reconnectable(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	switch connect.CodeOf(err) {
	case connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeResourceExhausted:
		return true
	default:
		return false
	}
}
