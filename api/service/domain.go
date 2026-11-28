package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/appdomain"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrDomainAlreadyExists = rpcerr.NewCoded(connect.CodeAlreadyExists, "domain already exists")
	ErrCannotRemovePrimary = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"make another domain primary before removing the primary domain",
	)
	errPlacementNotService = errors.New("domain placement has no service spec")
	errDomainWithoutPort   = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"a service without a port receives no traffic, so it cannot have a domain",
	)
)

type DomainServer struct {
	db          *pgxpool.Pool
	queries     genDb.Querier
	authz       *authz.Authorizer
	defaults    servicedefaults.Defaults
	silentAfter time.Duration
	counts      *configv1.CountLimits
}

func NewDomainServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	defaults servicedefaults.Defaults,
	clusterSilentAfter time.Duration,
	counts *configv1.CountLimits,
) *DomainServer {
	return &DomainServer{
		db:          db,
		queries:     queries,
		authz:       authz.New(queries),
		defaults:    defaults,
		silentAfter: clusterSilentAfter,
		counts:      counts,
	}
}

// ListAppDomains lists the app domains the clusters an environment deploys to serve.
func (s *DomainServer) ListAppDomains(
	ctx context.Context,
	req *domainv1.ListAppDomainsRequest,
) (*domainv1.ListAppDomainsResponse, error) {
	environmentID := uuid.MustParse(req.GetEnvironmentId())
	_, err := visibleEnvironment(ctx, s.queries, s.authz, environmentID, actions.ListAppDomains)
	if err != nil {
		return nil, err
	}
	eligible, err := eligibleClusters(ctx, s.queries, s.silentAfter, s.defaults.TargetPlatform)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	clusters := regionClusters(eligible)
	served := appdomain.Served(clusters)
	domains := make([]*domainv1.AppDomain, 0, len(served))
	for _, domain := range served {
		appDomain := &domainv1.AppDomain{Domain: domain}
		for _, cluster := range clusters {
			if slices.Contains(cluster.Domains, domain) {
				appDomain.Regions = append(appDomain.Regions, cluster.Region)
			}
		}
		domains = append(domains, appDomain)
	}
	return &domainv1.ListAppDomainsResponse{AppDomains: domains}, nil
}

// CreateResourceDomain adds a new domain to a resource
func (s *DomainServer) CreateResourceDomain(
	ctx context.Context,
	req *domainv1.CreateResourceDomainRequest,
) (*domainv1.CreateResourceDomainResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.AddDomain, resourceID); err != nil {
		return nil, err
	}
	environmentID, err := parseID("environment_id", req.GetEnvironmentId())
	if err != nil {
		return nil, err
	}
	fullDomain := req.GetDomain().GetSubdomain() + "." + req.GetDomain().GetAppDomain()

	var resourceDomain uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, envErr := resourceEnvironment(ctx, qtx, resourceID, environmentID); envErr != nil {
			return envErr
		}
		if lockErr := lockResourceDomains(ctx, qtx, resourceID, environmentID); lockErr != nil {
			return lockErr
		}
		appDomain, subdomain, checkErr := s.checkResourceDomain(ctx, qtx, resourceID, environmentID, fullDomain)
		if checkErr != nil {
			return fmt.Errorf("check the resource domain: %w", checkErr)
		}
		existing, countErr := qtx.GetResourceDomainCount(ctx, genDb.GetResourceDomainCountParams{
			ResourceID:    resourceID,
			EnvironmentID: environmentID,
		})
		if countErr != nil {
			return fmt.Errorf("count domains: %w", countErr)
		}
		if limitErr := checkResourceLimit(
			ctx, qtx, s.counts, limits.DomainsPerResource, resourceID, existing, 1,
		); limitErr != nil {
			return limitErr
		}
		hasPrimary, primaryErr := qtx.ResourceHasPrimaryDomain(ctx, genDb.ResourceHasPrimaryDomainParams{
			ResourceID:    resourceID,
			EnvironmentID: environmentID,
		})
		if primaryErr != nil {
			return fmt.Errorf("check primary domain: %w", primaryErr)
		}
		created, createErr := qtx.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
			ResourceID:     resourceID,
			EnvironmentID:  environmentID,
			Domain:         fullDomain,
			DomainSource:   genDb.DomainSourceApp,
			SubdomainLabel: &subdomain,
			AppDomain:      &appDomain,
			IsPrimary:      !hasPrimary,
		})
		if isDomainTaken(createErr) {
			return ErrDomainAlreadyExists
		}
		if createErr != nil {
			return fmt.Errorf("insert resource domain: %w", createErr)
		}
		resourceDomain = created
		if routeErr := s.refreshDomainRouting(ctx, qtx, resourceID, environmentID); routeErr != nil {
			return routeErr
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, environmentID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:          events.DomainCreated,
			EnvironmentID: new(environmentID),
			ResourceID:    new(resourceID),
			SubjectType:   events.SubjectDomain,
			SubjectID:     new(resourceDomain),
			Data: domainEventData(fullDomain, genDb.DomainSourceApp, !hasPrimary,
				map[string]any{events.FieldResourceID: resourceID.String()}),
		})
	})
	if err != nil {
		return nil, err
	}

	return &domainv1.CreateResourceDomainResponse{
		DomainId: resourceDomain.String(),
	}, nil
}

// UpdateResourceDomain updates a domain for a resource
func (s *DomainServer) UpdateResourceDomain(
	ctx context.Context,
	req *domainv1.UpdateResourceDomainRequest,
) (*domainv1.UpdateResourceDomainResponse, error) {
	domainID := uuid.MustParse(req.GetDomainId())

	domainRow, err := s.queries.GetResourceDomainByID(ctx, domainID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDomainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get resource domain: %w", err)
	}

	required := actions.New(actions.UpdateDomain, domainRow.ResourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrDomainNotFound); authErr != nil {
		return nil, authErr
	}

	changed := req.GetDomain() != "" && req.GetDomain() != domainRow.Domain

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockResourceDomains(ctx, qtx, domainRow.ResourceID, domainRow.EnvironmentID); lockErr != nil {
			return lockErr
		}
		var subdomainLabel *string
		var appDomain *string
		if changed {
			if domainRow.DomainSource == genDb.DomainSourceApp {
				domain, label, checkErr := s.checkResourceDomain(
					ctx,
					qtx,
					domainRow.ResourceID,
					domainRow.EnvironmentID,
					req.GetDomain(),
				)
				if checkErr != nil {
					return fmt.Errorf("check the resource domain: %w", checkErr)
				}
				subdomainLabel = &label
				appDomain = &domain
			}
		}

		if changed {
			_, updateErr := qtx.UpdateResourceDomain(ctx, genDb.UpdateResourceDomainParams{
				ID:             domainID,
				Domain:         req.GetDomain(),
				SubdomainLabel: subdomainLabel,
				AppDomain:      appDomain,
			})
			if isDomainTaken(updateErr) {
				return ErrDomainAlreadyExists
			}
			if updateErr != nil {
				return fmt.Errorf("update resource domain: %w", updateErr)
			}
			if routeErr := s.refreshDomainRouting(
				ctx,
				qtx,
				domainRow.ResourceID,
				domainRow.EnvironmentID,
			); routeErr != nil {
				return routeErr
			}
			if bumpErr := bumpEnvironmentRevision(ctx, qtx, domainRow.EnvironmentID); bumpErr != nil {
				return bumpErr
			}
		}
		return events.Record(ctx, qtx, events.Event{
			Type:          events.DomainUpdated,
			EnvironmentID: new(domainRow.EnvironmentID),
			ResourceID:    new(domainRow.ResourceID),
			SubjectType:   events.SubjectDomain,
			SubjectID:     new(domainID),
			Data:          domainEventData(req.GetDomain(), domainRow.DomainSource, domainRow.IsPrimary, nil),
		})
	})
	if err != nil {
		return nil, err
	}

	return &domainv1.UpdateResourceDomainResponse{
		DomainId: req.GetDomainId(),
	}, nil
}

// SetPrimaryResourceDomain sets which domain is primary for a resource
func (s *DomainServer) SetPrimaryResourceDomain(
	ctx context.Context,
	req *domainv1.SetPrimaryResourceDomainRequest,
) (*domainv1.SetPrimaryResourceDomainResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.SetPrimaryDomain, resourceID); err != nil {
		return nil, err
	}
	domainID := uuid.MustParse(req.GetDomainId())

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		domainRow, getErr := qtx.GetResourceDomainByID(ctx, domainID)
		if errors.Is(getErr, pgx.ErrNoRows) || (getErr == nil && domainRow.ResourceID != resourceID) {
			return ErrDomainNotFound
		}
		if getErr != nil {
			return fmt.Errorf("get domain: %w", getErr)
		}
		if lockErr := lockResourceDomains(ctx, qtx, resourceID, domainRow.EnvironmentID); lockErr != nil {
			return lockErr
		}
		clearParams := genDb.UpdateResourceDomainPrimaryParams{
			ResourceID:    resourceID,
			EnvironmentID: domainRow.EnvironmentID,
		}
		if clearErr := qtx.UpdateResourceDomainPrimary(ctx, clearParams); clearErr != nil {
			return fmt.Errorf("clear primary domain: %w", clearErr)
		}
		_, setErr := qtx.SetResourceDomainPrimary(ctx, genDb.SetResourceDomainPrimaryParams{
			ID:            domainID,
			ResourceID:    resourceID,
			EnvironmentID: domainRow.EnvironmentID,
		})
		if errors.Is(setErr, pgx.ErrNoRows) {
			return ErrDomainNotFound
		}
		if setErr != nil {
			return fmt.Errorf("set primary domain: %w", setErr)
		}
		if routeErr := s.refreshDomainRouting(ctx, qtx, resourceID, domainRow.EnvironmentID); routeErr != nil {
			return routeErr
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, domainRow.EnvironmentID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:          events.DomainUpdated,
			EnvironmentID: new(domainRow.EnvironmentID),
			ResourceID:    new(resourceID),
			SubjectType:   events.SubjectDomain,
			SubjectID:     new(domainID),
			Data:          map[string]any{events.FieldPrimary: true},
		})
	})
	if err != nil {
		return nil, err
	}

	return &domainv1.SetPrimaryResourceDomainResponse{
		ResourceId: req.GetResourceId(),
		DomainId:   req.GetDomainId(),
	}, nil
}

// DeleteResourceDomain removes a domain from a resource
func (s *DomainServer) DeleteResourceDomain(
	ctx context.Context,
	req *domainv1.DeleteResourceDomainRequest,
) (*domainv1.DeleteResourceDomainResponse, error) {
	domainID := uuid.MustParse(req.GetDomainId())

	domainRow, err := s.queries.GetResourceDomainByID(ctx, domainID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDomainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get resource domain: %w", err)
	}

	required := actions.New(actions.RemoveDomain, domainRow.ResourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrDomainNotFound); authErr != nil {
		return nil, authErr
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		lockErr := lockResourceDomains(ctx, qtx, domainRow.ResourceID, domainRow.EnvironmentID)
		if errors.Is(lockErr, ErrResourceNotFound) {
			return ErrDomainNotFound
		}
		if lockErr != nil {
			return lockErr
		}
		current, getErr := qtx.GetResourceDomainByID(ctx, domainID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return ErrDomainNotFound
		}
		if getErr != nil {
			return fmt.Errorf("get domain: %w", getErr)
		}
		if current.IsPrimary {
			count, countErr := qtx.GetResourceDomainCount(ctx, genDb.GetResourceDomainCountParams{
				ResourceID:    current.ResourceID,
				EnvironmentID: current.EnvironmentID,
			})
			if countErr != nil {
				return fmt.Errorf("count resource domains: %w", countErr)
			}
			if count > 1 {
				return ErrCannotRemovePrimary
			}
		}
		if deleteErr := qtx.DeleteResourceDomain(ctx, domainID); deleteErr != nil {
			return fmt.Errorf("delete resource domain: %w", deleteErr)
		}
		if routeErr := s.refreshDomainRouting(ctx, qtx, current.ResourceID, current.EnvironmentID); routeErr != nil {
			return routeErr
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, current.EnvironmentID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:          events.DomainDeleted,
			EnvironmentID: new(current.EnvironmentID),
			ResourceID:    new(current.ResourceID),
			SubjectType:   events.SubjectDomain,
			SubjectID:     new(domainID),
			Data:          domainEventData(current.Domain, current.DomainSource, current.IsPrimary, nil),
		})
	})
	if err != nil {
		return nil, err
	}

	return &domainv1.DeleteResourceDomainResponse{}, nil
}

// CheckDomainAvailability checks if a domain is available
func (s *DomainServer) CheckDomainAvailability(
	ctx context.Context,
	req *domainv1.CheckDomainAvailabilityRequest,
) (*domainv1.CheckDomainAvailabilityResponse, error) {
	eligible, err := eligibleClusters(ctx, s.queries, s.silentAfter, s.defaults.TargetPlatform)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	served := appdomain.Served(regionClusters(eligible))
	if _, _, matchErr := appdomain.Match(req.GetDomain(), served); matchErr != nil &&
		!errors.Is(matchErr, appdomain.ErrCustomDomain) {
		return nil, matchErr
	}
	result, err := s.queries.CheckDomainAvailability(ctx, req.GetDomain())
	if err != nil {
		return nil, fmt.Errorf("check domain availability: %w", err)
	}
	return &domainv1.CheckDomainAvailabilityResponse{
		IsAvailable: result,
	}, nil
}

func (s *DomainServer) checkResourceDomain(
	ctx context.Context,
	queries genDb.Querier,
	resourceID, environmentID uuid.UUID,
	hostname string,
) (string, string, error) {
	resource, err := queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrResourceNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("get resource %s: %w", resourceID, err)
	}
	config, err := resourceEnvironment(ctx, queries, resourceID, environmentID)
	if err != nil {
		return "", "", err
	}
	spec, err := converter.DeserializeResourceSpec(config.Spec, resource.Type)
	if err != nil {
		return "", "", fmt.Errorf("decode resource environment spec: %w", err)
	}
	deployments, err := listLiveDeploymentsForEnvironment(ctx, queries, resourceID, environmentID)
	if err != nil {
		return "", "", err
	}
	hasDeployment, hasPort := false, false
	for _, deployment := range deployments {
		hasDeployment = true
		current, specErr := currentServiceSpec(deployment.Deployment, resource.Type)
		if specErr != nil {
			return "", "", specErr
		}
		hasPort = hasPort || current.GetPort() != 0
	}
	if (!hasDeployment && spec.GetService().GetRouting().GetPort() == 0) || (hasDeployment && !hasPort) {
		return "", "", errDomainWithoutPort
	}
	placements, err := queries.ListResourceEnvironmentPlacements(
		ctx,
		genDb.ListResourceEnvironmentPlacementsParams{ResourceID: resourceID, EnvironmentID: environmentID},
	)
	if err != nil {
		return "", "", fmt.Errorf("list resource %s placements: %w", resourceID, err)
	}
	placed := make([]uuid.UUID, 0, len(placements))
	for _, placement := range placements {
		placed = append(placed, placement.ClusterID)
	}
	regions := enabledRegions(spec.GetService().GetRegions())
	check, err := resourceDomainCheck(
		ctx,
		queries,
		regions,
		placed,
		s.silentAfter,
		s.defaults.TargetPlatform,
	)
	if err != nil {
		return "", "", fmt.Errorf("list domain clusters: %w", err)
	}
	return appdomain.Check(hostname, check.clusters, check.targets)
}

// lockResourceDomains locks the environment that holds the domains and their resource.
func lockResourceDomains(ctx context.Context, qtx *genDb.Queries, resourceID, environmentID uuid.UUID) error {
	if _, err := lockEnvironment(ctx, qtx, environmentID); err != nil {
		return err
	}
	return lockResource(ctx, qtx, resourceID)
}

func (s *DomainServer) refreshDomainRouting(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
) error {
	placements, err := qtx.ListResourceEnvironmentPlacements(
		ctx,
		genDb.ListResourceEnvironmentPlacementsParams{ResourceID: resourceID, EnvironmentID: environmentID},
	)
	if err != nil {
		return fmt.Errorf("list domain placements: %w", err)
	}
	if len(placements) == 0 {
		return nil
	}
	hostname, err := primaryHostname(ctx, qtx, resourceID, environmentID)
	if err != nil {
		return err
	}
	for _, placement := range placements {
		payload, err := decodePlacement(placement)
		if err != nil {
			return err
		}
		if payload.AppSpec.ServiceSpec == nil || placement.DeploymentID == nil {
			return errPlacementNotService
		}
		service := payload.AppSpec.ServiceSpec
		if service.Routing == nil && hostname == "" {
			continue
		}
		if service.Routing != nil && service.Routing.HostName == hostname {
			continue
		}
		switch {
		case hostname == "":
			service.Routing = nil
		case service.Routing != nil:
			service.Routing.HostName = hostname
		default:
			deployment, err := qtx.GetDeploymentByID(ctx, *placement.DeploymentID)
			if err != nil {
				return fmt.Errorf("get domain deployment: %w", err)
			}
			deploymentService := &deploymentv1.ServiceDeploymentSpec{}
			if err := protojson.Unmarshal(deployment.Spec, deploymentService); err != nil {
				return fmt.Errorf("decode deployment routing: %w", err)
			}
			routing := deploymentRouting(deploymentService)
			service.Routing = converter.ProtoToRoutingSpec(routing, hostname, s.defaults)
		}
		if err := rewritePlacement(ctx, qtx, placement, payload); err != nil {
			return err
		}
	}
	return nil
}

// domainEventData is the audit data of a domain event: its hostname, its source and whether it is primary.
func domainEventData(domain string, source genDb.DomainSource, primary bool, extra map[string]any) map[string]any {
	data := map[string]any{
		events.FieldDomain:       domain,
		events.FieldDomainSource: string(source),
		events.FieldPrimary:      primary,
	}
	maps.Copy(data, extra)
	return data
}
