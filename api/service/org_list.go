package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz/actions"
	apiDb "github.com/team-loco/loco/api/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

// orgFilterSQL keeps the organizations a ListOrgs request matches. $1 is an ILIKE pattern or empty,
// $2 is the OrgState: 0 for all, 1 for active, 2 for suspended.
const orgFilterSQL = ` WHERE ($2::int <> 1 OR o.suspended_at IS NULL)
  AND ($2::int <> 2 OR o.suspended_at IS NOT NULL)
  AND ($1::text = '' OR o.name ILIKE $1 OR o.id::text ILIKE $1 OR EXISTS (
    SELECT 1 FROM user_scopes us JOIN users u ON u.id = us.user_id
    WHERE us.entity_type = 'organization' AND us.entity_id = o.id AND us.scope = 'admin'
      AND u.email ILIKE $1))`

var errOrgListToken = errors.New("invalid organization page token")

// orgListOrder is the order a ListOrgs request pages in.
type orgListOrder struct {
	ByName     bool `json:"b,omitempty"`
	Descending bool `json:"d,omitempty"`
}

// orgListCursor is the last row of a page: its name or creation time, as the order needs, and its ID.
type orgListCursor struct {
	orgListOrder
	Name      string    `json:"n,omitempty"`
	CreatedAt time.Time `json:"t,omitzero"`
	ID        uuid.UUID `json:"id"`
}

// key is the value the order compares rows by.
func (c orgListCursor) key() any {
	if c.ByName {
		return c.Name
	}
	return c.CreatedAt
}

// ListOrgs lists every organization in the installation. It requires system read.
func (s *OrgServer) ListOrgs(ctx context.Context, req *orgv1.ListOrgsRequest) (*orgv1.ListOrgsResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ListOrgs); err != nil {
		return nil, err
	}
	order := orgListOrder{
		ByName:     req.GetOrderBy() == orgv1.OrgOrder_ORG_ORDER_NAME,
		Descending: req.GetDescending() || req.GetOrderBy() == orgv1.OrgOrder_ORG_ORDER_UNSPECIFIED,
	}
	cursor, err := decodeOrgListCursor(req.GetPageToken())
	if err == nil && req.GetPageToken() != "" && cursor.orgListOrder != order {
		err = errOrgListToken
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	filter := []any{likePattern(req.GetQuery()), int32(req.GetState())}
	pageSize := normalizePageSize(req.GetPageSize())
	page, err := s.orgListPage(ctx, filter, order, cursor, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	response := &orgv1.ListOrgsResponse{}
	if len(page) > int(pageSize) {
		page = page[:pageSize]
		token, encodeErr := json.Marshal(page[len(page)-1])
		if encodeErr != nil {
			return nil, fmt.Errorf("encode organization page token: %w", encodeErr)
		}
		response.NextPageToken = base64.RawURLEncoding.EncodeToString(token)
	}
	if response.Orgs, err = s.orgSummaries(ctx, page); err != nil {
		return nil, err
	}
	countFn := func(ctx context.Context, limit int32) (int64, error) { return s.countOrgs(ctx, filter, limit) }
	response.TotalCount, err = totalCount(ctx, req.GetIncludeTotalCount(), countFn)
	if err != nil {
		return nil, fmt.Errorf("count organizations: %w", err)
	}
	return response, nil
}

func (s *OrgServer) countOrgs(ctx context.Context, filter []any, limit int32) (int64, error) {
	sql := "SELECT count(*) FROM (SELECT 1 FROM organizations o" + orgFilterSQL + " LIMIT $3) capped"
	var n int64
	err := s.db.QueryRow(ctx, apiDb.Named("CountOrgs", sql), append(filter, limit)...).Scan(&n)
	return n, err
}

func (s *OrgServer) orgListPage(
	ctx context.Context,
	filter []any,
	order orgListOrder,
	after orgListCursor,
	limit int32,
) ([]orgListCursor, error) {
	key, cast := "o.created_at", "timestamptz"
	if order.ByName {
		key, cast = "lower(o.name)", "text"
	}
	direction, compare := "ASC", ">"
	if order.Descending {
		direction, compare = "DESC", "<"
	}
	args := filter
	sql := "SELECT " + key + ", o.id FROM organizations o" + orgFilterSQL
	if after.ID != uuid.Nil {
		sql += " AND (" + key + ", o.id) " + compare + " ($3::" + cast + ", $4::uuid)"
		args = append(args, after.key(), after.ID)
	}
	sql += fmt.Sprintf(" ORDER BY %s %s, o.id %s LIMIT $%d", key, direction, direction, len(args)+1)
	rows, err := s.db.Query(ctx, apiDb.Named("ListOrgsPage", sql), append(args, limit)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (orgListCursor, error) {
		c := orgListCursor{orgListOrder: order}
		var err error
		if order.ByName {
			err = row.Scan(&c.Name, &c.ID)
		} else {
			err = row.Scan(&c.CreatedAt, &c.ID)
		}
		return c, err
	})
}

// orgSummaries loads the organizations of page, in page's order.
func (s *OrgServer) orgSummaries(ctx context.Context, page []orgListCursor) ([]*orgv1.OrgSummary, error) {
	ids := make([]uuid.UUID, len(page))
	for i, row := range page {
		ids[i] = row.ID
	}
	rows, err := s.queries.ListOrgSummariesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list organization summaries: %w", err)
	}
	admins, err := s.queries.ListOrgAdmins(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list organization admins: %w", err)
	}
	byID := make(map[uuid.UUID]*orgv1.OrgSummary, len(rows))
	for _, row := range rows {
		org := orgToProto(row.Organization)
		org.WorkspaceCount = new(row.WorkspaceCount)
		org.MemberCount = new(row.MemberCount)
		byID[row.Organization.ID] = &orgv1.OrgSummary{Organization: org, ResourceCount: row.ResourceCount}
	}
	for _, admin := range admins {
		if summary := byID[admin.OrgID]; summary != nil {
			summary.Owners = append(summary.Owners, &orgv1.User{
				Id: admin.ID.String(), Email: admin.Email, Name: derefString(admin.Name),
				AvatarUrl: derefString(admin.AvatarUrl),
			})
		}
	}
	out := make([]*orgv1.OrgSummary, 0, len(page))
	for _, row := range page {
		// An organization deleted between the two reads drops off this page.
		if summary := byID[row.ID]; summary != nil {
			out = append(out, summary)
		}
	}
	return out, nil
}

// decodeOrgListCursor reads a page token; the first page's empty token is the zero cursor.
func decodeOrgListCursor(token string) (orgListCursor, error) {
	var cursor orgListCursor
	if token == "" {
		return cursor, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor, errOrgListToken
	}
	if err = json.Unmarshal(raw, &cursor); err != nil || cursor.ID == uuid.Nil {
		return orgListCursor{}, errOrgListToken
	}
	return cursor, nil
}
