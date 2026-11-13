// Package org holds the locoadm commands that manage one organization.
package org

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/team-loco/loco/cmd/internal/auth"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

const flagReason = "reason"

var (
	errNoLimits     = errors.New("name at least one limit to set, such as --workspaces-per-org 500")
	errUnknownLimit = errors.New("unknown limit")
)

func BuildOrgCmd() *cobra.Command {
	org := &cobra.Command{Use: "org", Short: "Manage organizations (requires system admin)"}
	limits := &cobra.Command{Use: "limits", Short: "Show or override an organization's count limits"}
	limits.AddCommand(getCmd(), setCmd(), unsetCmd())
	org.AddCommand(limits)
	return org
}

func getCmd() *cobra.Command {
	return command(&cobra.Command{
		Use:   "get <org-id>",
		Short: "Print the organization's effective limits and which ones it overrides",
	}, false, func(cmd *cobra.Command, client orgv1connect.OrgServiceClient, orgID string) (proto.Message, error) {
		return client.GetOrgLimits(cmd.Context(), &orgv1.GetOrgLimitsRequest{OrgId: orgID})
	})
}

func setCmd() *cobra.Command {
	set := command(&cobra.Command{
		Use:     "set <org-id> --<limit> <value>... --reason <text>",
		Short:   "Override limits for the organization; the others keep their current value",
		Example: "  locoadm org limits set 0192... --workspaces-per-org 500 --reason \"large customer\"",
	}, true, func(cmd *cobra.Command, client orgv1connect.OrgServiceClient, orgID string) (proto.Message, error) {
		counts := &configv1.CountLimits{}
		var paths []string
		for _, field := range limitFields() {
			flag := flagName(field)
			if !cmd.Flags().Changed(flag) {
				continue
			}
			value, err := cmd.Flags().GetInt32(flag)
			if err != nil {
				return nil, err
			}
			counts.ProtoReflect().Set(field, protoreflect.ValueOfInt32(value))
			paths = append(paths, string(field.Name()))
		}
		if len(paths) == 0 {
			return nil, errNoLimits
		}
		return setLimits(cmd, client, orgID, counts, paths)
	})
	for _, field := range limitFields() {
		set.Flags().Int32(flagName(field), 0, "New "+strings.ReplaceAll(string(field.Name()), "_", " ")+" limit")
	}
	return set
}

func unsetCmd() *cobra.Command {
	return command(&cobra.Command{
		Use:     "unset <org-id> <limit>... --reason <text>",
		Short:   "Return limits to the install default",
		Example: "  locoadm org limits unset 0192... workspaces-per-org --reason \"contract ended\"",
		Args:    cobra.MinimumNArgs(2),
	}, true, func(cmd *cobra.Command, client orgv1connect.OrgServiceClient, orgID string) (proto.Message, error) {
		var paths []string
		for _, name := range cmd.Flags().Args()[1:] {
			field := limitField(name)
			if field == nil {
				return nil, fmt.Errorf("%w %q; use one of %s", errUnknownLimit, name, strings.Join(limitNames(), ", "))
			}
			paths = append(paths, string(field.Name()))
		}
		return setLimits(cmd, client, orgID, &configv1.CountLimits{}, paths)
	})
}

// command adds the host flag and, for a change, the required reason flag; it checks the
// organization ID, signs in and prints the response.
func command(
	c *cobra.Command,
	change bool,
	run func(*cobra.Command, orgv1connect.OrgServiceClient, string) (proto.Message, error),
) *cobra.Command {
	if c.Args == nil {
		c.Args = cobra.ExactArgs(1)
	}
	c.Flags().String("host", "", "API host URL")
	if change {
		c.Flags().String(flagReason, "", "Why the limits change; recorded in the organization's audit log")
		cobra.CheckErr(c.MarkFlagRequired(flagReason))
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if _, err := uuid.Parse(args[0]); err != nil {
			return fmt.Errorf("invalid organization ID: %w", err)
		}
		login, err := auth.ResolveLogin(cmd)
		if err != nil {
			return err
		}
		response, err := run(cmd, login.Clients.Org, args[0])
		if err != nil {
			return err
		}
		encoded, err := protojson.MarshalOptions{Indent: "  "}.Marshal(response)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
		return err
	}
	return c
}

func setLimits(
	cmd *cobra.Command,
	client orgv1connect.OrgServiceClient,
	orgID string,
	counts *configv1.CountLimits,
	paths []string,
) (proto.Message, error) {
	reason, err := cmd.Flags().GetString(flagReason)
	if err != nil {
		return nil, err
	}
	return client.SetOrgLimits(cmd.Context(), &orgv1.SetOrgLimitsRequest{
		OrgId:      orgID,
		Limits:     counts,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
		Reason:     reason,
	})
}

func limitFields() []protoreflect.FieldDescriptor {
	fields := (&configv1.CountLimits{}).ProtoReflect().Descriptor().Fields()
	all := make([]protoreflect.FieldDescriptor, fields.Len())
	for i := range fields.Len() {
		all[i] = fields.Get(i)
	}
	return all
}

func limitField(name string) protoreflect.FieldDescriptor {
	return (&configv1.CountLimits{}).ProtoReflect().Descriptor().Fields().
		ByName(protoreflect.Name(strings.ReplaceAll(name, "-", "_")))
}

func limitNames() []string {
	fields := limitFields()
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, flagName(field))
	}
	return names
}

func flagName(field protoreflect.FieldDescriptor) string {
	return strings.ReplaceAll(string(field.Name()), "_", "-")
}
