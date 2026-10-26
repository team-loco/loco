package cluster

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/rpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	clusterCreate    = "create"
	clusterJoinToken = "join-token"
	clusterList      = "list"
	clusterPause     = "pause"
	clusterResume    = "resume"
	clusterRemove    = "remove"
	clusterRestore   = "restore"

	flagName   = "name"
	flagRegion = "region"
	flagTier   = "tier"

	tierEnumPrefix = "ENVIRONMENT_TYPE_"
)

var errInvalidTier = errors.New("tier must be staging or production")

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
	operations := []string{
		clusterCreate, clusterJoinToken, clusterList, clusterPause, clusterResume, clusterRemove, clusterRestore,
	}
	for _, operation := range operations {
		command := &cobra.Command{
			Use:   operation + " <cluster-id>",
			Short: operation + " cluster",
			Args:  cobra.ExactArgs(1),
		}
		if operation == clusterList {
			command.Use = operation
			command.Args = cobra.NoArgs
		}
		if operation == clusterJoinToken {
			command.Short = "issue a one-time join token that enrolls an agent into the cluster"
		}
		if operation == clusterCreate {
			command.Use = operation
			command.Short = "create a pending cluster and print its one-time join token"
			command.Args = cobra.NoArgs
			command.Flags().String(flagName, "", "Cluster name")
			command.Flags().String(flagRegion, "", "Cluster region")
			command.Flags().String(flagTier, "", "Cluster tier: staging or production")
			for _, flag := range []string{flagName, flagRegion, flagTier} {
				cobra.CheckErr(command.MarkFlagRequired(flag))
			}
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
	case clusterCreate:
		request := &clusterv1.CreateClusterRequest{}
		fields := map[string]*string{flagName: &request.Name, flagRegion: &request.Region}
		for flag, field := range fields {
			value, err := cmd.Flags().GetString(flag)
			if err != nil {
				return nil, err
			}
			*field = value
		}
		tierFlag, err := cmd.Flags().GetString(flagTier)
		if err != nil {
			return nil, err
		}
		request.Tier, err = parseTier(tierFlag)
		if err != nil {
			return nil, err
		}
		return client.CreateCluster(ctx, request)
	case clusterJoinToken:
		return client.IssueJoinToken(ctx, &clusterv1.IssueJoinTokenRequest{ClusterId: args[0]})
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

func parseTier(value string) (environmentv1.EnvironmentType, error) {
	tier, ok := environmentv1.EnvironmentType_value[tierEnumPrefix+strings.ToUpper(value)]
	if !ok || tier == int32(environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED) {
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED, fmt.Errorf("%w: %q", errInvalidTier, value)
	}
	return environmentv1.EnvironmentType(tier), nil
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
