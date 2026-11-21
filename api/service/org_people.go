package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	personInvitation = "invitation"
	personMember     = "member"
)

// orgPeopleSQL lists invitations newest first, then the caller, then the other members by name.
// grp, k1 and k2 are that order as ascending keys, so a page token is the last row's keys. An
// invitation's k2 inverts its time-ordered ID, so invitations made in the same instant stay newest first.
// $1 org, $2 caller, $3 view, $4 workspace or NULL, $5 ILIKE pattern or empty.
const orgPeopleSQL = `WITH people AS (
  SELECT 'invitation'::text AS kind, i.id, 0 AS grp,
    -(EXTRACT(EPOCH FROM i.created_at) * 1000000)::bigint AS k1,
    translate(i.id::text, '0123456789abcdef', 'fedcba9876543210') AS k2
  FROM invitations i
  WHERE i.org_id = $1 AND i.claimed_at IS NULL AND i.revoked_at IS NULL
    AND $3::text <> 'members' AND $4::uuid IS NULL
    AND ($5::text = '' OR i.email ILIKE $5)
  UNION ALL
  SELECT 'member', u.id, CASE WHEN u.id = $2 THEN 1 ELSE 2 END, 0::bigint,
    lower(COALESCE(NULLIF(u.name, ''), u.email))
  FROM users u
  WHERE u.id IN (
      SELECT us.user_id FROM user_scopes us WHERE us.entity_type = 'organization' AND us.entity_id = $1)
    AND $3::text <> 'invitations'
    AND ($5::text = '' OR (COALESCE(u.name, '') || ' ' || u.email) ILIKE $5)
    AND ($4::uuid IS NULL
      OR EXISTS (SELECT 1 FROM user_scopes a WHERE a.user_id = u.id AND a.entity_type = 'organization'
                 AND a.entity_id = $1 AND a.scope = 'admin')
      OR EXISTS (SELECT 1 FROM user_scopes w WHERE w.user_id = u.id AND w.entity_type = 'workspace'
                 AND w.entity_id = $4::uuid))
)`

var errPeopleToken = errors.New("invalid people page token")

type personKey struct {
	Group int       `json:"g"`
	K1    int64     `json:"t"`
	K2    string    `json:"n"`
	ID    uuid.UUID `json:"id"`
}

type personRow struct {
	Kind string
	personKey
}

// ListOrgPeople lists an organization's members and open invitations in one order, so one page token
// moves through both.
func (s *OrgServer) ListOrgPeople(
	ctx context.Context,
	req *orgv1.ListOrgPeopleRequest,
) (*orgv1.ListOrgPeopleResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ListOrgMembers, orgID); err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	var workspaceID *uuid.UUID
	if req.GetWorkspaceId() != "" {
		if workspaceID, err = s.peopleWorkspace(ctx, orgID, req.GetWorkspaceId()); err != nil {
			return nil, err
		}
	}
	after, err := decodePersonKey(req.GetPageToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	args := []any{orgID, p.UserID, peopleView(req.GetView()), workspaceID, likePattern(req.GetQuery())}
	pageSize := normalizePageSize(req.GetPageSize())
	rows, err := s.peoplePage(ctx, args, after, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list organization people: %w", err)
	}
	response := &orgv1.ListOrgPeopleResponse{}
	if len(rows) > int(pageSize) {
		rows = rows[:pageSize]
		last, encodeErr := json.Marshal(rows[len(rows)-1].personKey)
		if encodeErr != nil {
			return nil, fmt.Errorf("encode people page token: %w", encodeErr)
		}
		response.NextPageToken = base64.RawURLEncoding.EncodeToString(last)
	}
	if response.People, err = s.people(ctx, orgID, rows); err != nil {
		return nil, err
	}
	if err = s.countPeople(ctx, req.GetIncludeTotalCount(), orgID, args, response); err != nil {
		return nil, err
	}
	return response, nil
}

// peopleWorkspace checks the workspace filter: the caller must read the workspace, and it must belong
// to the organization, since the filter tells who has access to it.
func (s *OrgServer) peopleWorkspace(ctx context.Context, orgID uuid.UUID, raw string) (*uuid.UUID, error) {
	workspaceID, err := parseID("workspace_id", raw)
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ListWorkspaceMembers, workspaceID); err != nil {
		return nil, err
	}
	parent, err := s.queries.GetOrganizationIDByWorkspaceID(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && parent != orgID {
		return nil, rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get the organization of workspace %s: %w", workspaceID, err)
	}
	return &workspaceID, nil
}

func (s *OrgServer) peoplePage(ctx context.Context, args []any, after personKey, limit int32) ([]personRow, error) {
	sql := orgPeopleSQL + " SELECT kind, id, grp, k1, k2 FROM people"
	if after.ID != uuid.Nil {
		sql += " WHERE (grp, k1, k2, id) > ($6, $7, $8, $9)"
		args = append(args, after.Group, after.K1, after.K2, after.ID)
	}
	sql += fmt.Sprintf(" ORDER BY grp, k1, k2, id LIMIT $%d", len(args)+1)
	rows, err := s.db.Query(ctx, sql, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (personRow, error) {
		var r personRow
		err := row.Scan(&r.Kind, &r.ID, &r.Group, &r.K1, &r.K2)
		return r, err
	})
}

// people loads the members and invitations rows name, in rows' order.
func (s *OrgServer) people(ctx context.Context, orgID uuid.UUID, rows []personRow) ([]*orgv1.OrgPerson, error) {
	var memberIDs, invitationIDs []uuid.UUID
	for _, row := range rows {
		if row.Kind == personMember {
			memberIDs = append(memberIDs, row.ID)
		} else {
			invitationIDs = append(invitationIDs, row.ID)
		}
	}
	members, err := s.queries.ListOrgMembersByUserIDs(ctx, genDb.ListOrgMembersByUserIDsParams{
		OrgID: orgID, UserIds: memberIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
	}
	byMember := make(map[uuid.UUID]*orgv1.OrgPerson, len(members))
	for _, m := range members {
		byMember[m.ID] = &orgv1.OrgPerson{Person: &orgv1.OrgPerson_Member{Member: &orgv1.OrgMember{
			User: &orgv1.User{
				Id: m.ID.String(), Email: m.Email, Name: derefString(m.Name), AvatarUrl: derefString(m.AvatarUrl),
			},
			Scopes: m.Scopes, JoinedAt: timestamppb.New(m.JoinedAt), LastLoginAt: optionalTimestamp(m.LastLoginAt),
		}}}
	}
	if err = s.addWorkspaceAccess(ctx, orgID, memberIDs, byMember); err != nil {
		return nil, err
	}
	invitations, err := s.queries.ListInvitationsByIDs(ctx, invitationIDs)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	byInvitation := make(map[uuid.UUID]*orgv1.OrgPerson, len(invitations))
	inviters := map[uuid.UUID]genDb.User{}
	for _, invitation := range invitations {
		inviter, known := inviters[invitation.InvitedBy]
		if !known {
			if inviter, err = invitationInviter(ctx, s.queries, invitation.InvitedBy); err != nil {
				return nil, fmt.Errorf("get inviter: %w", err)
			}
			inviters[invitation.InvitedBy] = inviter
		}
		byInvitation[invitation.ID] = &orgv1.OrgPerson{
			Person: &orgv1.OrgPerson_Invitation{Invitation: s.invitationProto(invitation, inviter)},
		}
	}
	out := make([]*orgv1.OrgPerson, 0, len(rows))
	for _, row := range rows {
		person := byMember[row.ID]
		if row.Kind == personInvitation {
			person = byInvitation[row.ID]
		}
		// A member who left or an invitation claimed between the two reads drops off this page.
		if person != nil {
			out = append(out, person)
		}
	}
	return out, nil
}

// addWorkspaceAccess fills each member's scopes in the workspaces the caller can read.
func (s *OrgServer) addWorkspaceAccess(
	ctx context.Context,
	orgID uuid.UUID,
	memberIDs []uuid.UUID,
	members map[uuid.UUID]*orgv1.OrgPerson,
) error {
	p, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	all, readable := s.authz.ReadableWorkspaces(ctx, p.Scopes, orgID)
	scopes, err := s.queries.ListMemberWorkspaceScopes(ctx, genDb.ListMemberWorkspaceScopesParams{
		OrgID: orgID, UserIds: memberIDs,
	})
	if err != nil {
		return fmt.Errorf("list member workspace scopes: %w", err)
	}
	for _, row := range scopes {
		person := members[row.UserID]
		if person == nil || !all && !slices.Contains(readable, row.WorkspaceID) {
			continue
		}
		person.Workspaces = append(person.Workspaces, &orgv1.MemberWorkspaceAccess{
			WorkspaceId: row.WorkspaceID.String(), WorkspaceName: row.WorkspaceName, Scopes: row.Scopes,
		})
	}
	return nil
}

func (s *OrgServer) countPeople(
	ctx context.Context,
	include bool,
	orgID uuid.UUID,
	args []any,
	response *orgv1.ListOrgPeopleResponse,
) error {
	var counts genDb.CountOrgPeopleRow
	total, err := totalCount(ctx, include, func(ctx context.Context, limit int32) (int64, error) {
		var n int64
		if err := s.db.QueryRow(ctx, "SELECT count(*) FROM ("+orgPeopleSQL+
			" SELECT 1 FROM people LIMIT $6) capped", append(args, limit)...).Scan(&n); err != nil {
			return 0, err
		}
		var err error
		counts, err = s.queries.CountOrgPeople(ctx, genDb.CountOrgPeopleParams{OrgID: orgID, MaxRows: limit})
		return n, err
	})
	if err != nil {
		return fmt.Errorf("count organization people: %w", err)
	}
	response.TotalCount = total
	response.MemberCount = cappedCount(counts.MemberCount)
	response.InvitationCount = cappedCount(counts.InvitationCount)
	if !include {
		return nil
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	all, readable := s.authz.ReadableWorkspaces(ctx, p.Scopes, orgID)
	byWorkspace, err := s.queries.CountOrgPeopleByWorkspace(ctx, orgID)
	if err != nil {
		return fmt.Errorf("count organization people by workspace: %w", err)
	}
	response.WorkspaceMemberCounts = make(map[string]int32, len(byWorkspace))
	for _, row := range byWorkspace {
		if all || slices.Contains(readable, row.WorkspaceID) {
			response.WorkspaceMemberCounts[row.WorkspaceID.String()] = cappedCount(row.MemberCount)
		}
	}
	return nil
}

// decodePersonKey reads a page token; the first page's empty token is the zero key.
func decodePersonKey(token string) (personKey, error) {
	var key personKey
	if token == "" {
		return key, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return key, errPeopleToken
	}
	if err = json.Unmarshal(raw, &key); err != nil || key.ID == uuid.Nil {
		return personKey{}, errPeopleToken
	}
	return key, nil
}

func peopleView(view orgv1.OrgPeopleView) string {
	switch view {
	case orgv1.OrgPeopleView_ORG_PEOPLE_VIEW_MEMBERS:
		return "members"
	case orgv1.OrgPeopleView_ORG_PEOPLE_VIEW_INVITATIONS:
		return "invitations"
	default:
		return "all"
	}
}

// likePattern matches query anywhere in a value, with LIKE's own wildcards escaped.
func likePattern(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query) + "%"
}
