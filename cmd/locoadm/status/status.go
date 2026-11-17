package status

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/auth"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	skewUnknown = "unknown"
	skewAhead   = "ahead"
)

type clusterStatus struct {
	Cluster     json.RawMessage `json:"cluster"`
	Enrollment  string          `json:"enrollment"`
	VersionSkew string          `json:"versionSkew"`
}

func Command() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Read cluster enrollment, heartbeats and administrators from the API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			config, err := login.Clients.Config.GetConfig(cmd.Context(), &configv1.GetConfigRequest{})
			if err != nil {
				return err
			}
			clusters, err := login.Clients.ClusterAdmin.ListClusters(cmd.Context(), &clusterv1.ListClustersRequest{})
			if err != nil {
				return err
			}
			admins, err := login.Clients.SystemAdmin.ListSystemAdmins(cmd.Context(), &adminv1.ListSystemAdminsRequest{})
			if err != nil {
				return err
			}
			report := struct {
				APIVersion      string          `json:"apiVersion"`
				UnclaimedAdmins []string        `json:"unclaimedAdmins"`
				Clusters        []clusterStatus `json:"clusters"`
			}{APIVersion: config.GetApiVersion(), UnclaimedAdmins: admins.GetUnclaimedEmails(), Clusters: []clusterStatus{}}
			for _, cluster := range clusters.GetClusters() {
				encoded, err := protojson.Marshal(cluster)
				if err != nil {
					return err
				}
				enrollment := "pending"
				if cluster.GetAgentKeyFingerprint() != "" {
					enrollment = "enrolled"
				}
				report.Clusters = append(
					report.Clusters,
					clusterStatus{
						Cluster:     encoded,
						Enrollment:  enrollment,
						VersionSkew: skew(config.GetApiVersion(), cluster.GetAgentVersion()),
					},
				)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		},
	}
	command.Flags().String("host", "", "API host URL")
	return command
}

func skew(api, agent string) string {
	api = "v" + strings.TrimPrefix(api, "v")
	agent = "v" + strings.TrimPrefix(agent, "v")
	if !semver.IsValid(api) || !semver.IsValid(agent) {
		return skewUnknown
	}
	if semver.Compare(agent, api) > 0 {
		return skewAhead
	}
	if semver.Major(api) != semver.Major(agent) {
		return "unsupported"
	}
	minor := func(version string) (int, error) {
		parts := strings.Split(semver.MajorMinor(version), ".")
		return strconv.Atoi(parts[1])
	}
	apiMinor, err := minor(api)
	if err != nil {
		return skewUnknown
	}
	agentMinor, err := minor(agent)
	if err != nil {
		return skewUnknown
	}
	if apiMinor-agentMinor > 1 {
		return "unsupported"
	}
	return "supported"
}
