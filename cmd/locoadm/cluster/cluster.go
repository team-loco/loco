package cluster

import (
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/rpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	clusterList    = "list"
	clusterPause   = "pause"
	clusterResume  = "resume"
	clusterRemove  = "remove"
	clusterRestore = "restore"
)

type dependencies struct {
	token  func(*cobra.Command) (*keychain.UserToken, error)
	client func(string, string) clusterv1connect.ClusterAdminServiceClient
}

func BuildClusterCmd() *cobra.Command {
	deps := dependencies{
		token: cmdutil.GetCurrentLocoToken,
		client: func(host, token string) clusterv1connect.ClusterAdminServiceClient {
			return rpc.ForHost(host, token).ClusterAdmin
		},
	}
	return newClusterCmd(deps)
}

func newClusterCmd(deps dependencies) *cobra.Command {
	cluster := &cobra.Command{Use: "cluster", Short: "Manage clusters (requires system admin)"}
	for _, operation := range []string{clusterList, clusterPause, clusterResume, clusterRemove, clusterRestore} {
		command := &cobra.Command{
			Use:   operation + " <cluster-id>",
			Short: operation + " cluster",
			Args:  cobra.ExactArgs(1),
		}
		if operation == clusterList {
			command.Use = operation
			command.Args = cobra.NoArgs
		}
		command.Flags().String("host", "", "API host URL")
		if operation == clusterRemove {
			command.Flags().Bool("force", false, "Retain workload records and remove a lost cluster")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				if _, err := uuid.Parse(args[0]); err != nil {
					return fmt.Errorf("invalid cluster ID: %w", err)
				}
			}
			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			token, err := deps.token(cmd)
			if err != nil {
				return err
			}
			client := deps.client(host, token.Token)
			response, err := call(cmd, client, operation, args)
			if err != nil {
				printWorkloads(cmd, err)
				return err
			}
			encoded, err := protojson.MarshalOptions{Indent: "  "}.Marshal(response)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
			return err
		}
		cluster.AddCommand(command)
	}
	return cluster
}

func call(
	cmd *cobra.Command,
	client clusterv1connect.ClusterAdminServiceClient,
	operation string,
	args []string,
) (proto.Message, error) {
	ctx := cmd.Context()
	switch operation {
	case clusterList:
		return client.ListClusters(ctx, &clusterv1.ListClustersRequest{})
	case clusterPause:
		return client.PauseCluster(ctx, &clusterv1.PauseClusterRequest{ClusterId: args[0]})
	case clusterResume:
		return client.ResumeCluster(ctx, &clusterv1.ResumeClusterRequest{ClusterId: args[0]})
	case clusterRestore:
		return client.RestoreCluster(ctx, &clusterv1.RestoreClusterRequest{ClusterId: args[0]})
	default:
		force, err := cmd.Flags().GetBool("force")
		if err != nil {
			return nil, err
		}
		return client.RemoveCluster(ctx, &clusterv1.RemoveClusterRequest{ClusterId: args[0], Force: force})
	}
}

func printWorkloads(cmd *cobra.Command, err error) {
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		return
	}
	for _, detail := range connectErr.Details() {
		value, decodeErr := connectproto.UnmarshalErrorDetail(detail)
		if decodeErr != nil {
			continue
		}
		workloads, ok := value.(*clusterv1.ClusterWorkloads)
		if !ok {
			continue
		}
		for _, workload := range workloads.GetWorkloads() {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", workload.GetKind(), workload.GetId())
		}
	}
}
