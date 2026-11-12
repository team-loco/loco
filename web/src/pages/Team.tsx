import { useMutation } from "@connectrpc/connect-query";
import { removeOrgMember, resendInvitation, setOrgMemberRole } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { OrgRole, type Invitation } from "@gen/loco/org/v1/org_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { ExternalLinkIcon, PlusIcon, SearchXIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { toast } from "sonner";

import { useAuth } from "@/auth/AuthProvider";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { userLabel } from "@/lib/user";
import { pagedRows } from "@/lib/paging";

import { DOCS_URL } from "./splash/links";
import { InviteDrawer } from "./team/InviteDrawer";
import { buildRows, roleOf, VIEWS, type Member, type Role, type ScopeName, type View } from "./team/model";
import { RevokeInvitationDialog } from "./team/RevokeInvitationDialog";
import { TeamTable } from "./team/TeamTable";
import { useTeamData } from "./team/useTeamData";
import { WorkspaceAccessDrawer } from "./team/WorkspaceAccessDrawer";
import { WorkspaceFilter } from "./team/WorkspaceFilter";

const PAGE_SIZES = [25, 50, 100] as const;
const NOW_TICK_MS = 60_000;
const ACCESS_DOCS = `${DOCS_URL}/operations/access/`;
const SCOPE_OF: Record<ScopeName, Scope> = { read: Scope.READ, write: Scope.WRITE, admin: Scope.ADMIN };

export function Team() {
	const { orgId = "" } = useParams<{ orgId: string }>();
	const [params, setParams] = useSearchParams();
	const { user } = useAuth();
	const meId = user?.id ?? "";
	const scopes = useMyScopes();
	const data = useTeamData(orgId);
	const now = useNow(NOW_TICK_MS).getTime();
	const resend = useMutation(resendInvitation);
	const changeRole = useMutation(setOrgMemberRole);
	const remove = useMutation(removeOrgMember);
	const [removing, setRemoving] = useState<{ userId: string; name: string } | null>(null);
	const [roleChange, setRoleChange] = useState<{ userId: string; name: string; role: Role } | null>(null);
	const view = VIEWS.find((v) => v.key === params.get("view"))?.key ?? "all";
	const q = params.get("q") ?? "";
	const wsFilter = params.get("ws") ?? "";
	const accessId = params.get("access");
	const [revoking, setRevoking] = useState<Invitation | null>(null);

	const canInvite = scopes.holds(orgId, "", Scope.ADMIN);
	const canChangeRoles = scopes.holds(orgId, "", Scope.ADMIN);
	const orgName = data.org?.name ?? "";
	const inviteOpen = canInvite && params.get("invite") === "1";
	const rows = buildRows(data.members, data.invitations, view, q, meId, wsFilter);
	const update = (patch: Record<string, string | null>) => {
		setParams(
			(prev) => {
				const p = new URLSearchParams(prev);
				for (const [k, v] of Object.entries(patch)) {
					if (v === null || v === "") p.delete(k);
					else p.set(k, v);
				}
				return p;
			},
			{ replace: true },
		);
	};
	const size = PAGE_SIZES.find((n) => n.toString() === params.get("size")) ?? PAGE_SIZES[0];
	const paged = pagedRows(
		rows,
		{ page: Number(params.get("page") ?? 0) || 0, size },
		(next) => {
			update({
				page: next.page === 0 ? null : next.page.toString(),
				size: next.size === PAGE_SIZES[0] ? null : next.size.toString(),
			});
		},
		PAGE_SIZES,
	);
	const resetPage = { page: null };
	const workspaceCounts = new Map(
		data.workspaces.map((w) => [
			w.id,
			data.members.filter((m) => roleOf(m.member.scopes) === "Admin" || m.workspaces.some((a) => a.id === w.id)).length,
		]),
	);
	const counts: Record<View, number> = {
		all: data.members.length + data.invitations.length,
		members: data.members.length,
		invitations: data.invitations.length,
	};
	const accessMember = data.members.find((m) => m.member.user?.id === accessId) ?? null;
	const justYou = !data.isLoading && data.members.length <= 1 && data.invitations.length === 0;

	const setInviteOpen = (open: boolean) => {
		update({ invite: open ? "1" : null });
	};

	const doResend = async (invitation: Invitation) => {
		try {
			await resend.mutateAsync({ id: invitation.id });
			toast(`Invitation renewed for ${invitation.email}`);
		} catch (err) {
			toastConnectError(err, "Failed to resend the invitation");
		} finally {
			await data.invalidateInvitations();
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
			await data.invalidateRoles(roleChange.userId === meId);
		} catch (err) {
			toastConnectError(err, "Failed to change the organization role");
		}
	};

	const doRemove = async () => {
		if (removing === null) return;
		try {
			await remove.mutateAsync({ orgId, userId: removing.userId });
			setRemoving(null);
			toast(`Removed ${removing.name} from ${orgName}`);
			await Promise.all([data.invalidateRoles(false), data.invalidateAccess()]);
		} catch (err) {
			toastConnectError(err, "Failed to remove the member");
		}
	};

	const inviteTitle = canInvite ? undefined : "Only organization admins can invite members";
	const inviteButton = (
		<Button
			disabled={!canInvite}
			title={inviteTitle}
			onClick={() => {
				setInviteOpen(true);
			}}
		>
			<PlusIcon />
			Invite
		</Button>
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
							update({ q: null, ...resetPage });
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
		if (data.error !== null) {
			return <div className="py-7 text-center text-fg3">{getErrorMessage(data.error, "Failed to load members")}</div>;
		}
		return (
			<>
				<TeamTable
					rows={paged.rows}
					now={now}
					isLoading={data.isLoading}
					accessLoading={data.accessLoading}
					empty={empty}
					actions={{
						meId,
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
							update({ access: m.member.user?.id ?? null });
						},
						onCopyLink: (invitation) => {
							void navigator.clipboard.writeText(invitation.link).then(() => {
								toast(`Copied the invite link for ${invitation.email}`);
							});
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
						className="flex flex-col items-center gap-2 border-t border-line2 px-6 py-8 text-center"
					>
						<h2 id="team-invite-prompt" className="m-0 text-xl font-normal tracking-[-0.02em]">
							It's just you so far
						</h2>
						<p className="m-0 max-w-[34em] leading-[1.55] text-fg3">
							Invite teammates by email. They join {orgName} as members the first time they sign in.
						</p>
						<span className="mt-1 flex flex-wrap justify-center gap-2">
							{inviteButton}
							<Button variant="outline" render={<a href={ACCESS_DOCS} target="_blank" rel="noopener" />}>
								Read the docs
								<ExternalLinkIcon className="text-fg3" />
							</Button>
						</span>
					</section>
				) : (
					<Pager {...paged.pager} />
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
						{!data.isLoading && <Badge tone="count">{data.members.length}</Badge>}
					</>
				}
				actions={inviteButton}
			/>
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
								update({ view: next.key === "all" ? null : next.key, ...resetPage });
							}}
						>
							{VIEWS.map((v) => (
								<ToggleGroupItem key={v.key} value={v.key} count={data.isLoading ? undefined : counts[v.key]}>
									{v.label}
								</ToggleGroupItem>
							))}
						</ToggleGroup>
						<WorkspaceFilter
							workspaces={data.workspaces}
							counts={workspaceCounts}
							value={wsFilter}
							onChange={(ws) => {
								update({ ws, ...resetPage });
							}}
						/>
					</>
				}
				search={
					<SearchInput
						value={q}
						onChange={(e) => {
							update({ q: e.target.value, ...resetPage });
						}}
						placeholder="Search by name or email"
						aria-label="Search people"
					/>
				}
			>
				{body}
			</Section>
			<InviteDrawer
				orgId={orgId}
				orgName={orgName}
				open={inviteOpen}
				onClose={() => {
					setInviteOpen(false);
				}}
				onInvited={() => {
					void data.invalidateInvitations();
				}}
			/>
			<WorkspaceAccessDrawer
				member={accessMember}
				workspaces={data.workspaces}
				holds={(workspaceId, s) => scopes.holds(orgId, workspaceId, SCOPE_OF[s])}
				onClose={() => {
					update({ access: null });
				}}
				onChanged={data.invalidateAccess}
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
				title={`Remove ${removing?.name ?? "member"} from ${orgName}?`}
				description={`${removing?.name ?? "They"} loses access to ${orgName} and its workspaces right away.`}
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
					void data.invalidateInvitations();
				}}
			/>
		</Page>
	);
}
