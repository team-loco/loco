package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/team-loco/loco/cmd/internal/gitops"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/team-loco/loco/cmd/internal/auth"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

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

var (
	errInvalidTier   = errors.New("tier must be staging or production")
	errAttachTimeout = errors.New("attach timeout must be positive")
)

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
	cluster.AddCommand(attachCmd())
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
		{
			use:   clusterJoinToken + " <cluster-id>",
			short: "Issue a one-time enrollment token",
			args:  cobra.ExactArgs(1),
			run: func(cmd *cobra.Command, c adminClient, args []string) (proto.Message, error) {
				ttl, err := tokenTTL(cmd)
				if err != nil {
					return nil, err
				}
				return c.IssueJoinToken(cmd.Context(), &clusterv1.IssueJoinTokenRequest{ClusterId: args[0], Ttl: ttl})
			},
		},
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
	tokenCommand := strings.Fields(op.use)[0] == clusterCreate || strings.Fields(op.use)[0] == clusterJoinToken
	var recipients, output, namespace, secretName string
	if tokenCommand {
		command.Flags().Duration("ttl", 0, "Join-token lifetime; omission uses the API default")
		command.Flags().
			StringVar(&recipients, "sops-recipient", "", "Comma-separated age recipients for the encrypted Secret")
		command.Flags().StringVar(&output, "output", "", "Write an encrypted join-token Secret to this file")
		command.Flags().StringVar(&namespace, "namespace", "loco-system", "Namespace of the worker join-token Secret")
		command.Flags().
			StringVar(&secretName, "secret-name", "loco-agent-join-token", "Name of the worker join-token Secret")
		command.MarkFlagsRequiredTogether("sops-recipient", "output")
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if tokenCommand {
			if _, err := tokenTTL(cmd); err != nil {
				return err
			}
			if output != "" {
				if err := gitops.Recipients(recipients); err != nil {
					return err
				}
			}
		}
		if len(args) > 0 {
			if _, err := uuid.Parse(args[0]); err != nil {
				return fmt.Errorf("invalid cluster ID: %w", err)
			}
		}
		login, err := auth.ResolveLogin(cmd)
		if err != nil {
			return err
		}
		response, err := op.run(cmd, login.Clients.ClusterAdmin, args)
		if err != nil {
			printWorkloads(cmd, err)
			return err
		}
		if tokenCommand && output != "" {
			var token *clusterv1.JoinToken
			var id string
			switch result := response.(type) {
			case *clusterv1.CreateClusterResponse:
				token = result.GetJoinToken()
				id = result.GetClusterId()
			case *clusterv1.IssueJoinTokenResponse:
				token = result.GetJoinToken()
				id = result.GetClusterId()
			}
			if token == nil {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Cluster", id, "is enrolled")
				return err
			}
			if err = gitops.WriteSecret(
				cmd.Context(),
				output,
				recipients,
				namespace,
				secretName,
				map[string]string{"join-token": token.GetToken()},
			); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Cluster %s: encrypted join token written to %s\n", id, output)
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
	request.Ttl, err = tokenTTL(cmd)
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

func tokenTTL(cmd *cobra.Command) (*durationpb.Duration, error) {
	if !cmd.Flags().Changed("ttl") {
		return nil, nil
	}
	ttl, err := cmd.Flags().GetDuration("ttl")
	if err != nil {
		return nil, err
	}
	if ttl <= time.Duration(0) {
		return nil, errors.New("ttl must be positive")
	}
	return durationpb.New(ttl), nil
}
