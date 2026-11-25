import { useMutation } from "@connectrpc/connect-query";
import {
	listOrgPeople,
	removeOrgMember,
	resendInvitation,
	setOrgMemberRole,
} from "@gen/loco/org/v1/org-OrgService_connectquery";
import { OrgPeopleView, OrgRole, type Invitation } from "@gen/loco/org/v1/org_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { LayersIcon, PlusIcon, SearchXIcon, XIcon } from "lucide-react";
import { keepPreviousData } from "@tanstack/react-query";
import { parseAsNumberLiteral, parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { useMemo, useState } from "react";
import { useParams } from "react-router";
import { toast } from "@/lib/toast";

import { useAuth } from "@/auth/AuthProvider";
import { DocsButton } from "@/components/design/DocsButton";
import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { EmptyState } from "@/components/design/EmptyState";
import { ErrorState } from "@/components/design/ErrorState";
import { PickFilter } from "@/components/design/FilterPopover";
import { LimitCount, LimitedAction } from "@/components/design/LimitedAction";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspaceFilterChoices } from "@/hooks/orgWorkspaces";
import { useNow } from "@/hooks/useNow";
import { useOpenInvitations } from "@/hooks/useOpenInvitations";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { useShownPage, useTokenPage } from "@/hooks/useTokenPage";
import { useCopy } from "@/hooks/useCopy";
import { toastConnectError } from "@/lib/error-handler";
import { limitReason, limitUsage } from "@/lib/limits";
import { userLabel } from "@/lib/user";
import { pageTokensParser, tokenPageLabel, tokenPager } from "@/lib/paging";

import { DOCS_URL } from "./splash/links";
import { InviteDrawer } from "./team/InviteDrawer";
import { teamRows, VIEWS, type Member, type Role, type ScopeName, type View } from "./team/model";
import { RevokeInvitationDialog } from "./team/RevokeInvitationDialog";
import { TeamArt } from "./team/TeamArt";
import { TeamTable } from "./team/TeamTable";
import { useTeamData } from "./team/useTeamData";
import { WorkspaceAccessDrawer } from "./team/WorkspaceAccessDrawer";
import { InlineCode } from "@/components/design/InlineCode";

const PAGE_SIZES = [25, 50, 100] as const;
const NOW_TICK_MS = 60_000;
const ACCESS_DOCS = `${DOCS_URL}/operations/access/`;
const SCOPE_OF: Record<ScopeName, Scope> = { read: Scope.READ, write: Scope.WRITE, admin: Scope.ADMIN };

const TEAM_PARAMS = {
	view: parseAsStringLiteral(VIEWS.map((v) => v.key)).withDefault("all"),
	q: parseAsString.withDefault(""),
	ws: parseAsString.withDefault(""),
	access: parseAsString,
	invite: parseAsStringLiteral(["1"]),
	pages: pageTokensParser,
	size: parseAsNumberLiteral(PAGE_SIZES).withDefault(PAGE_SIZES[0]),
};

export function Team() {
	const { orgId = "" } = useParams<{ orgId: string }>();
	const [params, setParams] = useQueryStates(TEAM_PARAMS, { history: "replace" });
	const { user } = useAuth();
	const [, copy] = useCopy();
	const meId = user?.id ?? "";
	const scopes = useMyScopes();
	const data = useTeamData(orgId);
	const wsChoices = useWorkspaceFilterChoices(orgId, params.ws);
	const limits = useOrgLimits(orgId);
	const now = useNow(NOW_TICK_MS).getTime();
	const resend = useMutation(resendInvitation);
	const changeRole = useMutation(setOrgMemberRole);
	const remove = useMutation(removeOrgMember);
	const [removing, setRemoving] = useState<{ userId: string; name: string } | null>(null);
	const [roleChange, setRoleChange] = useState<{ userId: string; name: string; role: Role } | null>(null);
	const { view, q, ws: wsFilter, access: accessId, size } = params;
	const [revoking, setRevoking] = useState<Invitation | null>(null);

	const canInvite = scopes.holds(orgId, "", Scope.ADMIN);
	const canChangeRoles = scopes.holds(orgId, "", Scope.ADMIN);
	const orgName = data.org?.name ?? "";
	const inviteOpen = canInvite && params.invite !== null;
	const people = useTokenPage(
		listOrgPeople,
		{
			orgId,
			pageSize: size,
			query: q.trim(),
			view: VIEWS.find((v) => v.key === view)?.view ?? OrgPeopleView.UNSPECIFIED,
			workspaceId: wsFilter,
		},
		params.pages,
		{ enabled: orgId !== "", placeholderData: keepPreviousData },
	);
	const isLoading = people.query.isPending;
	const peopleData = people.query.data;
	const rows = useMemo(() => teamRows(peopleData?.people ?? []), [peopleData]);
	const pager = tokenPager(params.pages, people.nextPageToken, (pages) => {
		if (!people.query.isPlaceholderData) void setParams({ pages });
	});
	const shown = useShownPage(
		pager.page,
		tokenPageLabel(pager.page, size, rows.length, people.total),
		people.query.isPlaceholderData,
	);
	const resetPage = { pages: null };
	const counted = people.counts;
	const workspaceCounts = new Map(Object.entries(counted?.workspaceMemberCounts ?? {}));
	const counts: Record<View, number> | undefined =
		counted === undefined
			? undefined
			: {
					all: counted.memberCount + counted.invitationCount,
					members: counted.memberCount,
					invitations: counted.invitationCount,
				};
	const accessMember =
		rows.flatMap((r) => (r.kind === "member" && r.member.member.user?.id === accessId ? [r.member] : []))[0] ?? null;
	const justYou = counted !== undefined && counted.memberCount <= 1 && counted.invitationCount === 0;
	const error = data.orgError ?? people.query.error;

	const setInviteOpen = (open: boolean) => {
		void setParams({ invite: open ? "1" : null });
	};

	const doResend = async (invitation: Invitation) => {
		try {
			await resend.mutateAsync({ id: invitation.id });
			toast(`Sent invite to ${invitation.email}`);
		} catch (err) {
			toastConnectError(err, "Failed to resend the invitation");
		} finally {
			await data.invalidatePeople();
		}
	};

	const doChangeRole = async () => {
		if (roleChange === null) return;
		try {
			await changeRole.mutateAsync({
				orgId,
				userId: roleChange.userId,
				role: roleChange.role === "Admin" ? OrgRole.ADMIN : OrgRole.MEMBER,
			});
			setRoleChange(null);
			toast("Organization role updated");
			await data.invalidatePeople();
		} catch (err) {
			toastConnectError(err, "Failed to change the organization role");
		}
	};

	const doRemove = async () => {
		if (removing === null) return;
		try {
			await remove.mutateAsync({ orgId, userId: removing.userId });
			setRemoving(null);
			toast(`Removed ${removing.name}`);
			await data.invalidatePeople();
		} catch (err) {
			toastConnectError(err, "Failed to remove the member");
		}
	};

	const memberUsage =
		counted === undefined || error !== null
			? null
			: limitUsage(counted.memberCount, limits?.membersPerOrg, "members", "organization");
	const openInvitations = useOpenInvitations(orgId);
	const invitationUsage =
		openInvitations === undefined || error !== null
			? null
			: limitUsage(openInvitations, limits?.invitationsPerOrg, "open invitations", "organization");
	const inviteBlocked = canInvite
		? (limitReason(memberUsage) ?? limitReason(invitationUsage))
		: "Only organization admins can invite members";
	const inviteButton = (
		<LimitedAction reason={inviteBlocked}>
			<Button
				disabled={inviteBlocked !== null}
				onClick={() => {
					setInviteOpen(true);
				}}
			>
				<PlusIcon />
				Invite
			</Button>
		</LimitedAction>
	);

	const empty =
		q.trim() !== "" ? (
			<EmptyState
				icon={<SearchXIcon />}
				title="Nobody matches"
				query={q.trim()}
				action={
					<Button
						variant="outline"
						onClick={() => {
							void setParams({ q: null, ...resetPage });
						}}
					>
						<XIcon />
						Clear search
					</Button>
				}
			/>
		) : (
			<div className="py-7 text-center text-fg3">No invitations</div>
		);

	const body = (() => {
		return (
			<>
				<TeamTable
					rows={rows}
					now={now}
					isLoading={isLoading}
					accessLoading={isLoading}
					empty={empty}
					actions={{
						meId,
						workspaceFilter: wsFilter,
						orgName,
						canManageInvitations: canInvite,
						canChangeRoles,
						onRole: (member, role) => {
							const target = member.member.user;
							if (target !== undefined) setRoleChange({ userId: target.id, name: userLabel(target), role });
						},
						onRemove: (member) => {
							const target = member.member.user;
							if (target !== undefined) setRemoving({ userId: target.id, name: userLabel(target) });
						},
						onAccess: (m: Member) => {
							void setParams({ access: m.member.user?.id ?? null });
						},
						onCopyLink: (invitation) => {
							copy("invite", invitation.link, `Copied the invite link for ${invitation.email}`);
						},
						onResend: (invitation) => {
							void doResend(invitation);
						},
						onRevoke: setRevoking,
					}}
				/>
				{justYou && q.trim() === "" ? (
					<section
						aria-labelledby="team-invite-prompt"
						className="mx-3 flex flex-col items-center gap-3.5 border-t border-line2 px-6 pt-11 pb-13 text-center max-md:mx-0"
					>
						<TeamArt
							name={user?.name ?? ""}
							email={user?.email ?? ""}
							disabled={inviteBlocked !== null}
							onInvite={() => {
								setInviteOpen(true);
							}}
						/>
						<h2 id="team-invite-prompt" className="m-0 mt-2.5 text-2xl font-normal tracking-[-0.03em]">
							It’s just you so far
						</h2>
						<p className="m-0 max-w-[34em] text-[14px] leading-[1.55] text-fg3">
							Invite people to <InlineCode>{orgName}</InlineCode> and choose which workspaces they can see.
						</p>
						<span className="mt-1 flex flex-wrap justify-center gap-2">
							{inviteButton}
							<DocsButton href={ACCESS_DOCS} />
						</span>
					</section>
				) : (
					<Pager
						{...pager}
						{...shown}
						total={rows.length}
						loading={isLoading}
						pageSizes={PAGE_SIZES}
						pageSize={size}
						onPageSize={(next) => {
							void setParams({ size: PAGE_SIZES.find((n) => n === next) ?? null, ...resetPage });
						}}
					/>
				)}
			</>
		);
	})();

	return (
		<Page>
			<PageHeader
				title={
					<>
						Team
						{counted !== undefined && error === null && <LimitCount usage={memberUsage} />}
					</>
				}
				actions={inviteButton}
			/>
			{error !== null ? (
				<ErrorState
					error={error}
					what="people"
					rpc={listOrgPeople}
					scope="org"
					onRetry={async () => await Promise.all([data.refetchOrg(), people.query.refetch()])}
				/>
			) : (
				<Section
					filters={
						<>
							<ToggleGroup
								variant="segmented"
								aria-label="Show"
								className="max-w-full overflow-x-auto"
								value={[view]}
								onValueChange={(v: string[]) => {
									const next = VIEWS.find((x) => x.key === v[0]);
									if (next === undefined) return;
									void setParams({ view: next.key, ...resetPage });
								}}
							>
								{VIEWS.map((v) => (
									<ToggleGroupItem key={v.key} value={v.key} count={counts?.[v.key]}>
										{v.label}
									</ToggleGroupItem>
								))}
							</ToggleGroup>
							<PickFilter
								icon={<LayersIcon />}
								label="Workspace"
								items={wsChoices.workspaces}
								onSearch={wsChoices.onSearch}
								placeholder="Find a workspace"
								width="w-[280px]"
								counts={workspaceCounts}
								value={wsFilter}
								onChange={(ws) => {
									void setParams({ ws, ...resetPage });
								}}
							/>
						</>
					}
					search={
						<SearchInput
							value={q}
							onChange={(e) => {
								void setParams({ q: e.target.value, ...resetPage });
							}}
							placeholder="Search by name or email"
							aria-label="Search people"
						/>
					}
				>
					{body}
				</Section>
			)}
			<InviteDrawer
				orgId={orgId}
				orgName={orgName}
				open={inviteOpen}
				onClose={() => {
					setInviteOpen(false);
				}}
				onInvited={() => {
					void data.invalidatePeople();
				}}
			/>
			<WorkspaceAccessDrawer
				orgId={orgId}
				member={accessMember}
				holds={(workspaceId, s) => scopes.holds(orgId, workspaceId, SCOPE_OF[s])}
				onClose={() => {
					void setParams({ access: null });
				}}
				onChanged={data.invalidatePeople}
			/>
			<ConfirmDialog
				open={roleChange !== null}
				title={`Change ${roleChange?.name ?? "member"} to ${roleChange?.role ?? "Member"}?`}
				description={
					roleChange?.role === "Admin"
						? "Admins can manage the organization and every workspace, invite members and change roles."
						: "Members keep their explicit workspace grants and lose organization admin access."
				}
				confirmLabel="Change role"
				pendingLabel="Changing…"
				pending={changeRole.isPending}
				onConfirm={() => {
					void doChangeRole();
				}}
				onClose={() => {
					setRoleChange(null);
				}}
			/>
			<ConfirmDialog
				open={removing !== null}
				title={`Remove ${removing?.name ?? "member"}?`}
				description={
					<>
						{removing?.name ?? "They"} loses access to <InlineCode>{orgName}</InlineCode> and its workspaces right away.
					</>
				}
				confirmLabel="Remove"
				pendingLabel="Removing…"
				pending={remove.isPending}
				onConfirm={() => {
					void doRemove();
				}}
				onClose={() => {
					setRemoving(null);
				}}
			/>
			<RevokeInvitationDialog
				invitation={revoking}
				onClose={() => {
					setRevoking(null);
				}}
				onRevoked={(email) => {
					setRevoking(null);
					toast(`Revoked the invitation to ${email}`);
					void data.invalidatePeople();
				}}
			/>
		</Page>
	);
}
