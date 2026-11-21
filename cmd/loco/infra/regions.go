package infra

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

// BuildRegionCmd creates the "region" command for choosing a region before writing loco.yaml.
func BuildRegionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "region", Short: "List the regions services can run in"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List each region's code and availability for the environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := resolveTarget(cmd.Context(), cmd)
			if err != nil {
				return err
			}
			config, err := t.Clients.Config.GetConfig(cmd.Context(), &configv1.GetConfigRequest{})
			if err != nil {
				return fmt.Errorf("could not fetch the default region from the API: %w", err)
			}
			regions, err := listRegions(cmd.Context(), t)
			if err != nil {
				return err
			}
			return writeRegions(cmd.OutOrStdout(), regions, config.GetDefaultRegion())
		},
	}
	addTargetFlags(list)
	cmd.AddCommand(list)
	return cmd
}

func listRegions(ctx context.Context, t cmdutil.Target) ([]*resourcev1.RegionInfo, error) {
	resp, err := t.Clients.Resource.ListRegions(
		ctx,
		&resourcev1.ListRegionsRequest{EnvironmentId: t.Environment.GetId()},
	)
	if err != nil {
		return nil, fmt.Errorf("could not list regions from the API: %w", err)
	}
	return resp.GetRegions(), nil
}

func writeRegions(w io.Writer, regions []*resourcev1.RegionInfo, defaultRegion string) error {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "CODE\tSTATUS")
	for _, region := range regions {
		status := "offline"
		if region.GetAvailability() == resourcev1.RegionAvailability_REGION_AVAILABILITY_AVAILABLE {
			status = "available"
		}
		if region.GetRegion() == defaultRegion {
			status += ", default"
		}
		fmt.Fprintf(table, "%s\t%s\n", region.GetRegion(), status)
	}
	return table.Flush()
}
