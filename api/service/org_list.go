package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz/actions"
	apiDb "github.com/team-loco/loco/api/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

// orgFilterSQL keeps the organizations a ListOrgs request matches. $1 is an ILIKE pattern or empty,
// $2 is "active", "suspended" or "all".
const orgFilterSQL = ` WHERE ($2::text <> 'active' OR o.suspended_at IS NULL)
  AND ($2::text <> 'suspended' OR o.suspended_at IS NOT NULL)
  AND ($1::text = '' OR o.name ILIKE $1 OR o.id::text ILIKE $1 OR EXISTS (
    SELECT 1 FROM user_scopes us JOIN users u ON u.id = us.user_id
    WHERE us.entity_type = 'organization' AND us.entity_id = o.id AND us.scope = 'admin'
      AND u.email ILIKE $1))`

var errOrgListToken = errors.New("invalid organization page token")

// orgSort is the key a ListOrgs request orders by; cast is the SQL type of the key.
type orgSort struct {
	expr string
	cast string
}

var (
	orgSortName    = orgSort{expr: "lower(o.name)", cast: "text"}
	orgSortCreated = orgSort{
		expr: "(extract(epoch FROM o.created_at) * 1000000)::bigint", cast: "bigint",
	}
)

type orgListCursor struct {
	Cast string    `json:"c"`
	Key  string    `json:"k"`
	ID   uuid.UUID `json:"id"`
}

// ListOrgs lists every organization in the installation. It requires system read.
func (s *OrgServer) ListOrgs(ctx context.Context, req *orgv1.ListOrgsRequest) (*orgv1.ListOrgsResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ListOrgs); err != nil {
		return nil, err
	}
	sort := orgSortCreated
	if req.GetOrderBy() == orgv1.OrgOrder_ORG_ORDER_NAME {
		sort = orgSortName
	}
	descending := req.GetDescending() || req.GetOrderBy() == orgv1.OrgOrder_ORG_ORDER_UNSPECIFIED
	cursor, err := decodeOrgListCursor(req.GetPageToken(), sort)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	filter := []any{likePattern(req.GetQuery()), orgStateFilter(req.GetState())}
	pageSize := normalizePageSize(req.GetPageSize())
	page, err := s.orgListPage(ctx, filter, sort, descending, cursor, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	response := &orgv1.ListOrgsResponse{}
	if len(page) > int(pageSize) {
		page = page[:pageSize]
		last := page[len(page)-1]
		last.Cast = sort.cast
		token, encodeErr := json.Marshal(last)
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
	sort orgSort,
	descending bool,
	after orgListCursor,
	limit int32,
) ([]orgListCursor, error) {
	direction, compare := "ASC", ">"
	if descending {
		direction, compare = "DESC", "<"
	}
	args := filter
	sql := "SELECT (" + sort.expr + ")::text, o.id FROM organizations o" + orgFilterSQL
	if after.ID != uuid.Nil {
		sql += " AND (" + sort.expr + ", o.id) " + compare + " ($3::" + sort.cast + ", $4::uuid)"
		args = append(args, after.Key, after.ID)
	}
	sql += fmt.Sprintf(" ORDER BY %s %s, o.id %s LIMIT $%d", sort.expr, direction, direction, len(args)+1)
	rows, err := s.db.Query(ctx, apiDb.Named("ListOrgsPage", sql), append(args, limit)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (orgListCursor, error) {
		var c orgListCursor
		err := row.Scan(&c.Key, &c.ID)
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

func orgStateFilter(state orgv1.OrgState) string {
	switch state {
	case orgv1.OrgState_ORG_STATE_ACTIVE:
		return "active"
	case orgv1.OrgState_ORG_STATE_SUSPENDED:
		return "suspended"
	default:
		return "all"
	}
}

// decodeOrgListCursor reads a page token; the first page's empty token is the zero cursor. A token
// made for another order is invalid.
func decodeOrgListCursor(token string, sort orgSort) (orgListCursor, error) {
	var cursor orgListCursor
	if token == "" {
		return cursor, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor, errOrgListToken
	}
	if err = json.Unmarshal(raw, &cursor); err != nil || cursor.ID == uuid.Nil || cursor.Cast != sort.cast {
		return orgListCursor{}, errOrgListToken
	}
	if sort.cast == "bigint" {
		if _, err = strconv.ParseInt(cursor.Key, 10, 64); err != nil {
			return orgListCursor{}, errOrgListToken
		}
	}
	return cursor, nil
}
