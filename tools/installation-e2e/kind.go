package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/team-loco/loco/internal/kindnode"
)

func (f *fixture) prepareKindNodes(ctx context.Context) error {
	nodes, err := command(ctx, f.root, nil, "kind", "get", "nodes", "--name", f.name)
	if err != nil {
		return err
	}
	for node := range strings.FieldsSeq(string(nodes)) {
		if _, err := command(
			ctx, f.root, nil, "docker", "exec", node, "sh", "-ec", kindnode.PrepareUserNamespaceSysfs,
		); err != nil {
			return fmt.Errorf("prepare kind node %s sysfs for pod user namespaces: %w", node, err)
		}
	}
	return nil
}
