package service

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/clusterassertion"
)

var (
	ErrInvalidAgentToken  = errors.New("invalid agent credential")
	errAssertionReplayed  = errors.New("cluster assertion was already used")
	errMissingBearerToken = errors.New("missing bearer token")
)

type ClusterAuthenticator struct {
	queries     genDb.Querier
	maxLifetime time.Duration
}

func NewClusterAuthenticator(queries genDb.Querier, maxLifetime time.Duration) *ClusterAuthenticator {
	return &ClusterAuthenticator{queries: queries, maxLifetime: maxLifetime}
}

func (a *ClusterAuthenticator) Authenticate(
	ctx context.Context,
	authHeader string,
) (genDb.GetEnrolledClusterRow, error) {
	token := extractBearerToken(authHeader)
	if token == "" {
		return genDb.GetEnrolledClusterRow{}, fmt.Errorf("%w: %w", ErrInvalidAgentToken, errMissingBearerToken)
	}
	issuer, err := clusterassertion.ClusterID(token)
	if err != nil {
		return genDb.GetEnrolledClusterRow{}, fmt.Errorf("%w: %w", ErrInvalidAgentToken, err)
	}
	id, err := uuid.Parse(issuer)
	if err != nil {
		return genDb.GetEnrolledClusterRow{}, fmt.Errorf("%w: %w", ErrInvalidAgentToken, err)
	}
	cluster, err := a.queries.GetEnrolledCluster(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.GetEnrolledClusterRow{}, fmt.Errorf("%w: %w", ErrInvalidAgentToken, ErrClusterNotFound)
	}
	if err != nil {
		return genDb.GetEnrolledClusterRow{}, fmt.Errorf("load cluster key: %w", err)
	}
	publicKey := ed25519.PublicKey(cluster.AgentPublicKey)
	claims, err := clusterassertion.Verify(token, publicKey, time.Now(), a.maxLifetime)
	if err != nil {
		return genDb.GetEnrolledClusterRow{}, fmt.Errorf("%w: %w", ErrInvalidAgentToken, err)
	}
	recorded, err := a.queries.RecordClusterAssertion(ctx, genDb.RecordClusterAssertionParams{
		ClusterID: id,
		Jti:       claims.ID,
		ExpiresAt: claims.Expiry.Add(clusterassertion.Leeway),
	})
	if err != nil {
		return genDb.GetEnrolledClusterRow{}, fmt.Errorf("record cluster assertion: %w", err)
	}
	if recorded == 0 {
		return genDb.GetEnrolledClusterRow{}, fmt.Errorf("%w: %w", ErrInvalidAgentToken, errAssertionReplayed)
	}
	return cluster, nil
}
