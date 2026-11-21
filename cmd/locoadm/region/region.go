// Package region lists the regions clusters run in. locoadm cluster create builds their codes.
package region

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/team-loco/loco/cmd/internal/auth"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

func BuildRegionCmd() *cobra.Command {
	region := &cobra.Command{Use: "region", Short: "List the regions clusters run in"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List every region's code and cluster count",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			response, err := login.Clients.RegionAdmin.ListRegions(cmd.Context(), &clusterv1.ListRegionsRequest{})
			if err != nil {
				return err
			}
			encoded, err := protojson.MarshalOptions{Indent: "  "}.Marshal(response)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
			return err
		},
	}
	list.Flags().String("host", "", "API host URL")
	region.AddCommand(list)
	return region
}
