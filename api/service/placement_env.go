package service

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/google/uuid"
	db "github.com/team-loco/loco/api/gen/db"
	planner "github.com/team-loco/loco/api/pkg/infra"
)

func placementIdentity(resourceID, clusterID uuid.UUID) []byte {
	return []byte(resourceID.String() + "/" + clusterID.String() + "/env")
}

func sealPlacementEnv(data []byte, cipher *planner.Cipher, resourceID, clusterID uuid.UUID) ([]byte, error) {
	var payload ApplicationPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.AppSpec == nil || payload.AppSpec.ServiceSpec == nil || payload.AppSpec.ServiceSpec.Deployment == nil {
		return data, nil
	}
	deployment := payload.AppSpec.ServiceSpec.Deployment
	if len(deployment.Env) == 0 {
		return data, nil
	}
	if cipher == nil {
		return nil, errors.New("placement environment encryption is required")
	}
	plaintext, err := json.Marshal(deployment.Env)
	if err != nil {
		return nil, err
	}
	sealed, err := cipher.Seal(plaintext, placementIdentity(resourceID, clusterID))
	if err != nil {
		return nil, err
	}
	payload.SealedEnv = sealed
	deployment.Env = nil
	return json.Marshal(payload)
}

func openPlacementEnv(data []byte, cipher *planner.Cipher, resourceID, clusterID uuid.UUID) ([]byte, error) {
	var payload ApplicationPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if len(payload.SealedEnv) == 0 {
		return data, nil
	}
	if cipher == nil || payload.AppSpec == nil || payload.AppSpec.ServiceSpec == nil ||
		payload.AppSpec.ServiceSpec.Deployment == nil {
		return nil, errors.New("encrypted placement environment cannot be opened")
	}
	plaintext, err := cipher.Open(payload.SealedEnv, placementIdentity(resourceID, clusterID))
	if err != nil {
		return nil, err
	}
	var variables map[string]string
	if err := json.Unmarshal(plaintext, &variables); err != nil {
		return nil, err
	}
	payload.AppSpec.ServiceSpec.Deployment.Env = variables
	payload.SealedEnv = nil
	return json.Marshal(payload)
}

func storeResourceVariables(
	ctx context.Context,
	q *db.Queries,
	resourceID uuid.UUID,
	values map[string]string,
	cipher *planner.Cipher,
) error {
	if cipher == nil {
		return errors.New("resource variable encryption is required")
	}
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	encrypted, err := cipher.Seal(data, []byte(resourceID.String()+"/variables"))
	if err != nil {
		return err
	}
	return q.UpdateInfraResourceVariables(
		ctx,
		db.UpdateInfraResourceVariablesParams{ID: resourceID, VariableValues: encrypted},
	)
}

func loadResourceVariables(resource db.Resource, cipher *planner.Cipher) (map[string]string, error) {
	if len(resource.VariableValues) == 0 {
		return nil, nil
	}
	if cipher == nil {
		return nil, errors.New("resource variable encryption is required")
	}
	data, err := cipher.Open(resource.VariableValues, []byte(resource.ID.String()+"/variables"))
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if err = json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (s *ResourceServer) resourceVariableKeys(ctx context.Context, resource db.Resource) ([]string, error) {
	values, err := loadResourceVariables(resource, s.cipher)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string)
	maps.Copy(names, values)
	deployments, err := s.queries.ListActiveDeploymentsForResource(ctx, resource.ID)
	if err != nil {
		return nil, err
	}
	for _, deployment := range deployments {
		current, currentErr := desiredEnv(ctx, s.queries, resource.ID, deployment.ClusterID, s.cipher)
		if currentErr != nil {
			return nil, currentErr
		}
		maps.Copy(names, current)
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, nil
}
