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
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
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

	flagName        = "name"
	flagRegion      = "region"
	flagCountry     = "country"
	flagSubdivision = "subdivision"
	flagLocal       = "local"
	flagForce       = "force"
)

type adminClient = clusterv1connect.ClusterAdminServiceClient

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
			short: "Create a pending cluster and deliver its one-time join token",
			args:  cobra.NoArgs,
			flags: func(c *cobra.Command) {
				c.Flags().String(flagName, "", "Cluster name; a new region's first cluster defaults to the region code")
				c.Flags().String(flagRegion, "", "Code of an existing region to add the cluster to, such as us-ca-1")
				c.Flags().String(flagCountry, "", "Create a region in this country, such as us")
				c.Flags().String(flagSubdivision, "", "State or province of the new region, such as ca")
				c.Flags().
					Bool(flagLocal, false, "Add the cluster to the development region local-1, creating it if needed")
				c.MarkFlagsOneRequired(flagRegion, flagCountry, flagLocal)
				c.MarkFlagsMutuallyExclusive(flagRegion, flagCountry, flagLocal)
				c.MarkFlagsRequiredTogether(flagCountry, flagSubdivision)
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
	var output, namespace, secretName, kubeContext string
	if tokenCommand {
		command.Flags().
			StringVar(&kubeContext, "kube-context", "", "Apply the join-token Secret to this Kubernetes context")
		command.Flags().Duration("ttl", 0, "Join-token lifetime; omission uses the API default")
		command.Flags().StringVarP(&output, "output", "o", "json", "Output format: json or secret")
		command.MarkFlagsMutuallyExclusive("kube-context", "output")
		command.Flags().StringVar(&namespace, "namespace", "loco-system", "Namespace of the worker join-token Secret")
		command.Flags().
			StringVar(&secretName, "secret-name", "loco-agent-join-token", "Name of the worker join-token Secret")
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if tokenCommand {
			if _, err := tokenTTL(cmd); err != nil {
				return err
			}
			if output != "json" && output != "secret" {
				return errors.New("output must be json or secret")
			}
		}
		if len(args) > 0 {
			if _, err := uuid.Parse(args[0]); err != nil {
				return fmt.Errorf("invalid cluster ID: %w", err)
			}
		}
		var worker *workerEnrollment
		if tokenCommand && cmd.Flags().Changed("kube-context") {
			if kubeContext == "" {
				return errors.New("kube-context must not be empty")
			}
			var err error
			worker, err = enrollmentClient(cmd.Context(), kubeContext, namespace, secretName)
			if err != nil {
				return err
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
		if created, ok := response.(*clusterv1.CreateClusterResponse); ok {
			suggestDefaultRegion(cmd, login.Clients, created.GetRegion())
		}
		if tokenCommand && (output == "secret" || worker != nil) {
			var token *clusterv1.JoinToken
			var clusterID, region string
			switch result := response.(type) {
			case *clusterv1.CreateClusterResponse:
				token = result.GetJoinToken()
				clusterID = result.GetClusterId()
				region = " in region " + result.GetRegion()
			case *clusterv1.IssueJoinTokenResponse:
				token = result.GetJoinToken()
				clusterID = result.GetClusterId()
			}
			if token == nil {
				return errors.New("cluster is enrolled; no join token was issued")
			}
			if worker != nil {
				if err = worker.apply(cmd.Context(), clusterID, token); err != nil {
					return err
				}
				_, err = fmt.Fprintf(
					cmd.OutOrStdout(),
					"Applied join-token Secret %s/%s to context %s for cluster %s%s\n",
					namespace,
					secretName,
					kubeContext,
					clusterID,
					region,
				)
				return err
			}
			data, encodeErr := gitops.Secret(namespace, secretName, map[string]string{"join-token": token.GetToken()})
			if encodeErr != nil {
				return encodeErr
			}
			_, err = cmd.OutOrStdout().Write(data)
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
	flags := map[string]string{}
	for _, flag := range []string{flagName, flagRegion, flagCountry, flagSubdivision} {
		value, err := cmd.Flags().GetString(flag)
		if err != nil {
			return nil, err
		}
		flags[flag] = value
	}
	local, err := cmd.Flags().GetBool(flagLocal)
	if err != nil {
		return nil, err
	}
	request := &clusterv1.CreateClusterRequest{RequestId: uuid.NewString(), Name: flags[flagName]}
	if flags[flagRegion] != "" {
		request.Target = &clusterv1.CreateClusterRequest_Region{Region: flags[flagRegion]}
	} else {
		request.Target = &clusterv1.CreateClusterRequest_NewRegion{NewRegion: &clusterv1.NewRegion{
			Country:     flags[flagCountry],
			Subdivision: flags[flagSubdivision],
			Local:       local,
		}}
	}
	request.Ttl, err = tokenTTL(cmd)
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

// suggestDefaultRegion prints a LOCO_DEFAULT_REGION hint when the API has no default and region is the only one.
func suggestDefaultRegion(cmd *cobra.Command, clients *rpc.Clients, region string) {
	config, err := clients.Config.GetConfig(cmd.Context(), &configv1.GetConfigRequest{})
	if err != nil || config.GetDefaultRegion() != "" {
		return
	}
	listed, err := clients.RegionAdmin.ListRegions(cmd.Context(), &clusterv1.ListRegionsRequest{})
	if err != nil || len(listed.GetRegions()) != 1 || listed.GetRegions()[0].GetCode() != region {
		return
	}
	fmt.Fprintf(
		cmd.ErrOrStderr(),
		"Set LOCO_DEFAULT_REGION=%[1]s on the API to make %[1]s the default region for new services.\n",
		region,
	)
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
