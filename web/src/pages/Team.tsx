import { useMutation } from "@connectrpc/connect-query";
import { resendInvitation } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Invitation } from "@gen/loco/org/v1/org_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { ExternalLinkIcon, PlusIcon, SearchXIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { toast } from "sonner";

import { useAuth } from "@/auth/AuthProvider";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { EmptyHero } from "@/components/design/EmptyHero";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { usePagedRows } from "@/lib/paging";

import { DOCS_URL } from "./splash/links";
import { InviteDrawer } from "./team/InviteDrawer";
import { buildRows, invitationBadge, VIEWS, type Member, type ScopeName, type View } from "./team/model";
import { RevokeInvitationDialog } from "./team/RevokeInvitationDialog";
import { TeamArt } from "./team/TeamArt";
import { TeamTable } from "./team/TeamTable";
import { useTeamData } from "./team/useTeamData";
import { WorkspaceAccessDialog } from "./team/WorkspaceAccessDialog";

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
	const [view, setView] = useState<View>("all");
	const [q, setQ] = useState("");
	const [accessId, setAccessId] = useState<string | null>(null);
	const [revoking, setRevoking] = useState<Invitation | null>(null);

	const canInvite = scopes.holds(orgId, "", Scope.ADMIN);
	const orgName = data.org?.name ?? "";
	const inviteOpen = canInvite && params.get("invite") === "1";
	const rows = buildRows(data.members, data.invitations, view, q, meId);
	const paged = usePagedRows(rows, PAGE_SIZES);
	const counts: Record<View, number> = {
		all: data.members.length + data.invitations.length,
		members: data.members.length,
		invitations: data.invitations.length,
	};
	const accessMember = data.members.find((m) => m.member.user?.id === accessId) ?? null;
	const justYou = !data.isLoading && data.members.length <= 1 && data.invitations.length === 0;

	const setInviteOpen = (open: boolean) => {
		setParams(
			(prev) => {
				const p = new URLSearchParams(prev);
				if (open) p.set("invite", "1");
				else p.delete("invite");
				return p;
			},
			{ replace: true },
		);
	};

	const doResend = async (invitation: Invitation) => {
		try {
			const res = await resend.mutateAsync({ id: invitation.id });
			const label = res.invitation === undefined ? "Invite sent" : invitationBadge(res.invitation.status).label;
			toast(`${invitation.email}: ${label}`);
		} catch (err) {
			toastConnectError(err, "Failed to resend the invitation");
		} finally {
			await data.invalidateInvitations();
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
							setQ("");
							paged.reset();
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
		if (justYou && q.trim() === "") {
			return (
				<EmptyHero
					art={<TeamArt />}
					title="It's just you so far"
					subtitle={`Invite teammates by email. They join ${orgName} as members the first time they sign in.`}
					className="rounded-none border-t border-line2"
				>
					<span className="flex flex-wrap justify-center gap-2">
						{inviteButton}
						<Button variant="outline" render={<a href={ACCESS_DOCS} target="_blank" rel="noopener" />}>
							Read the docs
							<ExternalLinkIcon className="text-fg3" />
						</Button>
					</span>
				</EmptyHero>
			);
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
						onAccess: (m: Member) => {
							setAccessId(m.member.user?.id ?? null);
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
				<Pager {...paged.pager} />
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
					<ToggleGroup
						variant="segmented"
						aria-label="Show"
						className="max-w-full overflow-x-auto"
						value={[view]}
						onValueChange={(v: string[]) => {
							const next = VIEWS.find((x) => x.key === v[0]);
							if (next === undefined) return;
							setView(next.key);
							paged.reset();
						}}
					>
						{VIEWS.map((v) => (
							<ToggleGroupItem key={v.key} value={v.key} count={data.isLoading ? undefined : counts[v.key]}>
								{v.label}
							</ToggleGroupItem>
						))}
					</ToggleGroup>
				}
				search={
					<SearchInput
						value={q}
						onChange={(e) => {
							setQ(e.target.value);
							paged.reset();
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
			<WorkspaceAccessDialog
				member={accessMember}
				workspaces={data.workspaces}
				holds={(workspaceId, s) => scopes.holds(orgId, workspaceId, SCOPE_OF[s])}
				onClose={() => {
					setAccessId(null);
				}}
				onChanged={data.invalidateAccess}
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
