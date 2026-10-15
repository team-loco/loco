package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/service"
)

const clusterCommand = "cluster"
const clusterList = "list"

var errClusterUsage = errors.New("usage: loco-api cluster list | pause|resume|remove|restore <cluster-id> [--force]")

func runClusterCommand(ctx context.Context, databaseURL string, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errClusterUsage
	}
	operation := args[0]
	if operation != clusterList && operation != service.ClusterPause && operation != service.ClusterResume &&
		operation != service.ClusterRemove &&
		operation != service.ClusterRestore {
		return errClusterUsage
	}
	force := len(args) == 3 && args[len(args)-1] == "--force" && operation == service.ClusterRemove
	if operation == clusterList {
		if len(args) != 1 {
			return errClusterUsage
		}
	} else {
		if len(args) != 2 && !force {
			return errClusterUsage
		}
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	encoder := json.NewEncoder(output)
	if operation == clusterList {
		clusters, listErr := genDb.New(pool).ListOperatorClusters(ctx)
		if listErr != nil {
			return fmt.Errorf("list clusters: %w", listErr)
		}
		return encoder.Encode(clusters)
	}
	id, err := uuid.Parse(args[1])
	if err != nil {
		return fmt.Errorf("invalid cluster ID: %w", err)
	}
	result, operationErr := service.ManageCluster(ctx, pool, operation, id, force)
	if len(result.Workloads) > 0 {
		if err := encoder.Encode(result); err != nil {
			return err
		}
	}
	if operationErr != nil {
		return operationErr
	}
	if len(result.Workloads) == 0 {
		return encoder.Encode(result)
	}
	return nil
}
