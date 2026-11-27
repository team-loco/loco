package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
)

func (s *ResourceServer) domainPublications(
	ctx context.Context,
	resourceIDs []uuid.UUID,
) (map[string]*domainv1.DomainPublication, error) {
	rows, err := s.queries.ListDomainPublicationStates(ctx, resourceIDs)
	if err != nil {
		return nil, fmt.Errorf("list domain publication states: %w", err)
	}
	publications := make(map[string]*domainv1.DomainPublication, len(rows))
	for _, row := range rows {
		state := domainv1.PublicationState_PUBLICATION_STATE_PENDING
		switch {
		case !row.Managed:
			state = domainv1.PublicationState_PUBLICATION_STATE_OPERATOR_MANAGED
		case row.Message != "":
			state = domainv1.PublicationState_PUBLICATION_STATE_ERROR
		case row.ClusterCount > 0 && row.Revision == row.AcknowledgedRevision:
			state = domainv1.PublicationState_PUBLICATION_STATE_PUBLISHED
		}
		publications[row.Domain] = &domainv1.DomainPublication{
			State:                state,
			Revision:             row.Revision,
			AcknowledgedRevision: row.AcknowledgedRevision,
			Message:              row.Message,
		}
	}
	return publications, nil
}
