package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/rpc"
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
	flagForce  = "force"

	tierEnumPrefix = "ENVIRONMENT_TYPE_"
)

type adminClient = clusterv1connect.ClusterAdminServiceClient

var errInvalidTier = errors.New("tier must be staging or production")

// operation describes one cluster subcommand. Every operation names its own RPC
// in run, so adding one cannot fall through to another.
type operation struct {
	use   string
	short string
	args  cobra.PositionalArgs
	flags func(*cobra.Command)
	run   func(cmd *cobra.Command, client adminClient, args []string) (proto.Message, error)
}

func BuildClusterCmd() *cobra.Command {
	cluster := &cobra.Command{Use: "cluster", Short: "Manage clusters (requires system admin)"}
	for _, op := range operations() {
		cluster.AddCommand(op.command())
	}
	return cluster
}

func operations() []operation {
	byID := func(
		name, short string,
		run func(context.Context, adminClient, string) (proto.Message, error),
	) operation {
		return operation{
			use:   name + " <cluster-id>",
			short: short,
			args:  cobra.ExactArgs(1),
			run: func(cmd *cobra.Command, client adminClient, args []string) (proto.Message, error) {
				return run(cmd.Context(), client, args[0])
			},
		}
	}
	return []operation{
		{
			use:   clusterCreate,
			short: "create a pending cluster and print its one-time join token",
			args:  cobra.NoArgs,
			flags: func(c *cobra.Command) {
				c.Flags().String(flagName, "", "Cluster name")
				c.Flags().String(flagRegion, "", "Cluster region")
				c.Flags().String(flagTier, "", "Cluster tier: staging or production")
				for _, flag := range []string{flagName, flagRegion, flagTier} {
					cobra.CheckErr(c.MarkFlagRequired(flag))
				}
			},
			run: createCluster,
		},
		byID(clusterJoinToken, "issue a one-time join token that enrolls an agent into the cluster",
			func(ctx context.Context, c adminClient, id string) (proto.Message, error) {
				return c.IssueJoinToken(ctx, &clusterv1.IssueJoinTokenRequest{ClusterId: id})
			}),
		{
			use:   clusterList,
			short: "list cluster",
			args:  cobra.NoArgs,
			run: func(cmd *cobra.Command, c adminClient, _ []string) (proto.Message, error) {
				return c.ListClusters(cmd.Context(), &clusterv1.ListClustersRequest{})
			},
		},
		byID(clusterPause, "pause cluster",
			func(ctx context.Context, c adminClient, id string) (proto.Message, error) {
				return c.PauseCluster(ctx, &clusterv1.PauseClusterRequest{ClusterId: id})
			}),
		byID(clusterResume, "resume cluster",
			func(ctx context.Context, c adminClient, id string) (proto.Message, error) {
				return c.ResumeCluster(ctx, &clusterv1.ResumeClusterRequest{ClusterId: id})
			}),
		{
			use:   clusterRemove + " <cluster-id>",
			short: "remove cluster",
			args:  cobra.ExactArgs(1),
			flags: func(c *cobra.Command) {
				c.Flags().Bool(flagForce, false, "Retain workload records and remove a lost cluster")
			},
			run: removeCluster,
		},
		byID(clusterRestore, "restore cluster",
			func(ctx context.Context, c adminClient, id string) (proto.Message, error) {
				return c.RestoreCluster(ctx, &clusterv1.RestoreClusterRequest{ClusterId: id})
			}),
	}
}

func (op operation) command() *cobra.Command {
	command := &cobra.Command{Use: op.use, Short: op.short, Args: op.args}
	command.Flags().String("host", "", "API host URL")
	if op.flags != nil {
		op.flags(command)
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			if _, err := uuid.Parse(args[0]); err != nil {
				return fmt.Errorf("invalid cluster ID: %w", err)
			}
		}
		login, err := cmdutil.ResolveLogin(cmd)
		if err != nil {
			return err
		}
		response, err := op.run(cmd, login.Clients.ClusterAdmin, args)
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
	return command
}

func createCluster(
	cmd *cobra.Command,
	client adminClient,
	_ []string,
) (proto.Message, error) {
	request := &clusterv1.CreateClusterRequest{RequestId: uuid.NewString()}
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
	return client.CreateCluster(cmd.Context(), request)
}

func removeCluster(
	cmd *cobra.Command,
	client adminClient,
	args []string,
) (proto.Message, error) {
	force, err := cmd.Flags().GetBool(flagForce)
	if err != nil {
		return nil, err
	}
	return client.RemoveCluster(cmd.Context(), &clusterv1.RemoveClusterRequest{ClusterId: args[0], Force: force})
}

func parseTier(value string) (environmentv1.EnvironmentType, error) {
	tier, ok := environmentv1.EnvironmentType_value[tierEnumPrefix+strings.ToUpper(value)]
	if !ok || tier == int32(environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED) {
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED, fmt.Errorf("%w: %q", errInvalidTier, value)
	}
	return environmentv1.EnvironmentType(tier), nil
}

func printWorkloads(cmd *cobra.Command, err error) {
	workloads, ok := rpc.ErrorDetail[*clusterv1.ClusterWorkloads](err)
	if !ok {
		return
	}
	for _, workload := range workloads.GetWorkloads() {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", workload.GetKind(), workload.GetId())
	}
}
