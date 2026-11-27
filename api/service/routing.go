package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	routingv1 "github.com/team-loco/loco/gen/go/loco/routing/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errPublisherSession = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"publisher session is active elsewhere or has expired",
	)
	errPublicationRevision = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"publication revision or publisher session is no longer current",
	)
	errRoutingCursor = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"routing cursor is ahead of the publisher; restart from zero",
	)
)

type RoutingServer struct {
	pool           *pgxpool.Pool
	authz          *authz.Authorizer
	sessionSeconds int32
	batchSize      int32
	drainSeconds   int32
}

func NewRoutingServer(
	pool *pgxpool.Pool,
	queries *genDb.Queries,
	sessionSeconds, batchSize, drainSeconds int32,
) *RoutingServer {
	return &RoutingServer{
		pool:           pool,
		authz:          authz.New(queries),
		sessionSeconds: sessionSeconds,
		batchSize:      batchSize,
		drainSeconds:   drainSeconds,
	}
}

func (s *RoutingServer) BindPublisher(
	ctx context.Context,
	req *routingv1.BindPublisherRequest,
) (*routingv1.BindPublisherResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.BindRoutingPublisher); err != nil {
		return nil, err
	}
	domain := req.GetAppDomain()
	if domain != strings.ToLower(domain) || len(validation.IsDNS1123Subdomain(domain)) != 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "app_domain must be a lower-case DNS hostname")
	}
	for _, prefix := range req.GetReservedPrefixes() {
		if prefix == "" || len(validation.IsDNS1123Label(prefix+"x")) != 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, "reserved prefixes must begin a valid DNS label")
		}
	}
	var publisher genDb.RoutePublisher
	err := withTx(ctx, s.pool, func(qtx *genDb.Queries) error {
		if err := qtx.LockRoutingAssignments(ctx); err != nil {
			return err
		}
		conflict, err := qtx.CheckPublisherReservations(
			ctx,
			genDb.CheckPublisherReservationsParams{AppDomain: domain, Prefixes: req.GetReservedPrefixes()},
		)
		if err != nil {
			return err
		}
		if conflict {
			return connect.NewError(
				connect.CodeFailedPrecondition,
				"reserved prefixes overlap assigned application names",
			)
		}
		publisher, err = qtx.BindRoutePublisher(
			ctx,
			genDb.BindRoutePublisherParams{AppDomain: domain, ReservedPrefixes: req.GetReservedPrefixes()},
		)
		if err != nil {
			return err
		}
		return nil
	})
	if isUniqueViolation(err, "route_publishers_app_domain_key") {
		return nil, connect.NewError(connect.CodeAlreadyExists, "app domain already has a publisher binding")
	}
	if err != nil {
		return nil, fmt.Errorf("bind routing publisher: %w", err)
	}
	return &routingv1.BindPublisherResponse{PublisherId: publisher.ID.String()}, nil
}

func (s *RoutingServer) ListRouteIntents(
	ctx context.Context,
	req *routingv1.ListRouteIntentsRequest,
) (*routingv1.ListRouteIntentsResponse, error) {
	id, err := parseID("publisher_id", req.GetPublisherId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ReadRouteIntents, id); err != nil {
		return nil, err
	}
	// Listing renews the exclusive writer session, so read permission alone cannot claim it.
	if err = authorize(ctx, s.authz, actions.ReportRoutePublication, id); err != nil {
		return nil, err
	}
	session, err := parseID("session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	response := &routingv1.ListRouteIntentsResponse{
		NextSequence:   req.GetAfterSequence(),
		SessionSeconds: s.sessionSeconds,
	}
	err = withTx(ctx, s.pool, func(qtx *genDb.Queries) error {
		publisher, renewErr := qtx.RenewRoutePublisherSession(
			ctx,
			genDb.RenewRoutePublisherSessionParams{ID: id, SessionID: &session, SessionSeconds: s.sessionSeconds},
		)
		if errors.Is(renewErr, pgx.ErrNoRows) {
			return errPublisherSession
		}
		if renewErr != nil {
			return fmt.Errorf("renew publisher session: %w", renewErr)
		}
		if req.GetAfterSequence() < 0 || req.GetAfterSequence() > publisher.ChangeSequence {
			return errRoutingCursor
		}
		rows, listErr := qtx.ListRouteIntents(
			ctx,
			genDb.ListRouteIntentsParams{
				PublisherID:    id,
				ChangeSequence: req.GetAfterSequence(),
				Limit:          s.batchSize,
			},
		)
		if listErr != nil {
			return fmt.Errorf("list routing intents: %w", listErr)
		}
		response.HasMore = len(rows) == int(s.batchSize)
		for _, row := range rows {
			response.Intents = append(response.Intents, routeIntent(row))
			response.NextSequence = row.ChangeSequence
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *RoutingServer) ReportPublication(
	ctx context.Context,
	req *routingv1.ReportPublicationRequest,
) (*routingv1.ReportPublicationResponse, error) {
	id, err := parseID("publisher_id", req.GetPublisherId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ReportRoutePublication, id); err != nil {
		return nil, err
	}
	session, err := parseID("session_id", req.GetSessionId())
	if err != nil {
		return nil, err
	}
	publication, err := parseID("publication_id", req.GetPublicationId())
	if err != nil {
		return nil, err
	}
	published := req.GetOutcome() == routingv1.PublicationOutcome_PUBLICATION_OUTCOME_PUBLISHED
	if !published && req.GetOutcome() != routingv1.PublicationOutcome_PUBLICATION_OUTCOME_ERROR {
		return nil, connect.NewError(connect.CodeInvalidArgument, "publication outcome is required")
	}
	message := req.GetMessage()
	if published {
		message = ""
	} else if message == "" {
		message = "Routing publication failed"
	}
	err = withTx(ctx, s.pool, func(qtx *genDb.Queries) error {
		if _, lockErr := qtx.LockRoutePublisher(ctx, id); lockErr != nil {
			return fmt.Errorf("lock routing publisher: %w", lockErr)
		}
		_, reportErr := qtx.ReportRoutePublication(
			ctx,
			genDb.ReportRoutePublicationParams{
				ID:           publication,
				PublisherID:  id,
				SessionID:    &session,
				Revision:     req.GetRevision(),
				Published:    published,
				Message:      message,
				DrainSeconds: s.drainSeconds,
			},
		)
		if errors.Is(reportErr, pgx.ErrNoRows) {
			return errPublicationRevision
		}
		if reportErr != nil {
			return fmt.Errorf("report route publication: %w", reportErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &routingv1.ReportPublicationResponse{}, nil
}

func routeIntent(row genDb.PublicRoutePublication) *routingv1.RouteIntent {
	clusters := make([]string, len(row.ClusterIds))
	for i, id := range row.ClusterIds {
		clusters[i] = id.String()
	}
	return &routingv1.RouteIntent{PublicationId: row.ID.String(), Revision: row.Revision, Hostname: row.Hostname,
		ResourceId: row.ResourceID.String(), EnvironmentId: row.EnvironmentID.String(), ClusterIds: clusters,
		Preference: "nearest", Withdrawing: row.Withdrawing, ChangeSequence: row.ChangeSequence}
}
