import type { Invitation } from "@gen/loco/org/v1/org_pb";
import { CheckIcon, ChevronDownIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuTrigger,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
} from "@/components/design/DropdownMenu";
import { LevelChip } from "@/components/design/LevelChip";
import { RowMenu } from "@/components/design/RowMenu";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { Skeleton } from "@/components/design/Skeleton";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { agoLabel, formatMonthYear, tsMs } from "@/lib/time";
import { userLabel } from "@/lib/user";
import { cn } from "@/lib/utils";

import { MemberAvatar } from "./MemberAvatar";
import { levelOf, ROLE_HINT, roleLabel, roleOf, untilLabel, type Member, type Role, type TeamRow } from "./model";

export interface RowActions {
	meId: string;
	workspaceFilter: string;
	orgName: string;
	canManageInvitations: boolean;
	canChangeRoles: boolean;
	onAccess: (member: Member) => void;
	onRole: (member: Member, role: Role) => void;
	onRemove: (member: Member) => void;
	onCopyLink: (invitation: Invitation) => void;
	onResend: (invitation: Invitation) => void;
	onRevoke: (invitation: Invitation) => void;
}

function RoleItems({ role, heading = false, onPick }: { role: Role; heading?: boolean; onPick: (role: Role) => void }) {
	return (
		<DropdownMenuGroup>
			{heading && <DropdownMenuLabel>Role</DropdownMenuLabel>}
			{(["Admin", "Member"] as const).map((next) => (
				<DropdownMenuItem
					key={next}
					disabled={next === role}
					className="items-start data-disabled:cursor-default data-disabled:opacity-100"
					onClick={() => {
						onPick(next);
					}}
				>
					<span className="flex min-w-0 flex-1 flex-col gap-0.5">
						<span>{next}</span>
						<span className="text-[12px] text-fg3">{ROLE_HINT[next]}</span>
					</span>
					<CheckIcon className={next === role ? "mt-0.5" : "invisible"} />
				</DropdownMenuItem>
			))}
		</DropdownMenuGroup>
	);
}

function roleTitle(isMe: boolean, canChangeRoles: boolean): string | undefined {
	if (!canChangeRoles) return "Only organization admins can change roles";
	if (isMe) return "Another admin can change your role";
	return undefined;
}

function Dash() {
	return <span className="text-fg4">—</span>;
}

function memberName(member: Member): string {
	const user = member.member.user;
	return user === undefined ? "" : userLabel(user);
}

function Person({ row, now, actions }: { row: TeamRow; now: number; actions: RowActions }) {
	if (row.kind === "invitation") {
		const { invitation } = row;
		const inviter = invitation.invitedBy;
		const sent = agoLabel(tsMs(invitation.createdAt), now);
		return (
			<span className="flex min-w-0 items-center gap-2.5">
				<MemberAvatar name="" email={invitation.email} avatarUrl="" className="size-[30px] shrink-0" />
				<span className="flex min-w-0 flex-col gap-px">
					<CellText text={invitation.email} className="text-[13.5px] font-medium" />
					<CellText
						text={
							inviter === undefined ? `invited ${sent}` : `invited by ${inviter.email || userLabel(inviter)}, ${sent}`
						}
						className="font-mono text-[11.5px] text-fg3"
					/>
				</span>
			</span>
		);
	}
	const user = row.member.member.user;
	return (
		<span className="flex min-w-0 items-center gap-2.5">
			<MemberAvatar
				name={user?.name ?? ""}
				email={user?.email ?? ""}
				avatarUrl={user?.avatarUrl ?? ""}
				className="size-[30px] shrink-0"
			/>
			<span className="flex min-w-0 flex-col gap-px">
				<span className="flex min-w-0 items-center gap-2 text-[13.5px] font-medium">
					<CellText text={memberName(row.member)} />
					{user?.id === actions.meId && <Badge tone="pill">you</Badge>}
				</span>
				<CellText text={user?.email ?? ""} className="font-mono text-[11.5px] text-fg3" />
			</span>
		</span>
	);
}

function RoleCell({ member, actions }: { member: Member; actions: RowActions }) {
	const isMe = member.member.user?.id === actions.meId;
	const role = roleOf(member.member.scopes);
	const roleLocked = isMe || !actions.canChangeRoles;
	// A locked role shows as plain text whose padding hangs into the cell's, so its label lines up with the column head; an editable one is an outlined button.
	return (
		<span className="flex min-w-0">
			<DropdownMenu>
				<DropdownMenuTrigger
					render={
						<Button
							variant="ghost"
							size="xs"
							disabled={roleLocked}
							aria-label={`Change role for ${memberName(member)}`}
							title={roleTitle(isMe, actions.canChangeRoles)}
							className={cn(
								"h-7 gap-1.5 border-line2 bg-background px-2 text-[13px] text-foreground hover:border-fg3 hover:bg-background aria-expanded:border-fg3 aria-expanded:bg-background dark:hover:bg-background",
								"disabled:-ml-2 disabled:border-transparent disabled:bg-transparent disabled:opacity-100 disabled:hover:border-transparent disabled:hover:bg-transparent",
							)}
						/>
					}
				>
					{role}
					{!roleLocked && <ChevronDownIcon className="text-fg4" />}
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" className="w-[260px]">
					<RoleItems
						role={role}
						heading
						onPick={(next) => {
							actions.onRole(member, next);
						}}
					/>
				</DropdownMenuContent>
			</DropdownMenu>
		</span>
	);
}

function Workspaces({
	member,
	accessLoading,
	actions,
}: {
	member: Member;
	accessLoading: boolean;
	actions: RowActions;
}) {
	const role = roleOf(member.member.scopes);
	const granted = member.workspaces.toSorted(
		(a, b) =>
			Number(b.id === actions.workspaceFilter) - Number(a.id === actions.workspaceFilter) ||
			levelOf(b.scopes) - levelOf(a.scopes) ||
			a.name.localeCompare(b.name),
	);
	const first = granted[0];
	const openAccess = () => {
		actions.onAccess(member);
	};
	if (role === "Admin") {
		return (
			<span title="All workspaces, Admin" className="flex min-w-0">
				<LevelChip level={3} name="All workspaces" nameFirst />
			</span>
		);
	}
	if (first === undefined) {
		return accessLoading ? (
			<Skeleton className="h-5 w-32" />
		) : (
			<button type="button" onClick={openAccess} className="text-[12.5px] text-fg3 hover:text-foreground">
				No access
			</button>
		);
	}
	return (
		<span className="flex min-w-0 items-center gap-1">
			<LevelChip level={levelOf(first.scopes)} name={first.name} nameFirst />
			{granted.length > 1 && (
				<Button
					variant="outline"
					size="xs"
					onClick={openAccess}
					aria-label={`All workspace access for ${memberName(member)}`}
					className="shrink-0 font-mono text-[11.5px] text-fg2"
				>
					+{(granted.length - 1).toLocaleString()} more
				</Button>
			)}
		</span>
	);
}

function Joined({ row, now }: { row: TeamRow; now: number }) {
	const pref = useTimePreferences();
	if (row.kind === "invitation") {
		const expires = tsMs(row.invitation.expiresAt);
		return <>{expires <= now ? `expired ${agoLabel(expires, now)}` : `expires ${untilLabel(expires, now)}`}</>;
	}
	const joined = row.member.member.joinedAt;
	return joined ? <>{formatMonthYear(tsMs(joined), pref)}</> : <Dash />;
}

function MemberMenuItems({ member, actions }: { member: Member; actions: RowActions }) {
	const [picking, setPicking] = useState(false);
	const role = roleOf(member.member.scopes);
	if (picking) {
		return (
			<RoleItems
				role={role}
				heading
				onPick={(next) => {
					actions.onRole(member, next);
				}}
			/>
		);
	}
	return (
		<>
			<DropdownMenuGroup>
				<DropdownMenuLabel className="max-w-[260px] truncate">{memberName(member)}</DropdownMenuLabel>
				<DropdownMenuItem
					disabled={!actions.canChangeRoles}
					closeOnClick={false}
					onClick={() => {
						setPicking(true);
					}}
				>
					Change role
				</DropdownMenuItem>
				{role === "Member" && (
					<DropdownMenuItem
						onClick={() => {
							actions.onAccess(member);
						}}
					>
						Workspace access
					</DropdownMenuItem>
				)}
			</DropdownMenuGroup>
			<DropdownMenuSeparator />
			<DropdownMenuItem
				variant="destructive"
				disabled={!actions.canChangeRoles}
				title={actions.canChangeRoles ? undefined : "Only organization admins can remove members"}
				onClick={() => {
					actions.onRemove(member);
				}}
			>
				Remove from {actions.orgName}
			</DropdownMenuItem>
		</>
	);
}

function MemberMenu({ member, actions }: { member: Member; actions: RowActions }) {
	if (member.member.user?.id === actions.meId) return null;
	return (
		<RowMenu label={`Actions for ${memberName(member)}`}>
			<MemberMenuItems member={member} actions={actions} />
		</RowMenu>
	);
}

function InvitationMenu({ invitation, now, actions }: { invitation: Invitation; now: number; actions: RowActions }) {
	const expired = tsMs(invitation.expiresAt) <= now;
	const manageTitle = actions.canManageInvitations ? undefined : "Only organization admins can change invitations";
	return (
		<RowMenu label={`Actions for invite to ${invitation.email}`} title={invitation.email}>
			<DropdownMenuGroup>
				<DropdownMenuItem
					onClick={() => {
						actions.onCopyLink(invitation);
					}}
				>
					Copy invite link
				</DropdownMenuItem>
				<DropdownMenuItem
					disabled={!actions.canManageInvitations}
					title={manageTitle}
					onClick={() => {
						actions.onResend(invitation);
					}}
				>
					{expired ? "Send a new invite" : "Resend invite"}
				</DropdownMenuItem>
			</DropdownMenuGroup>
			<DropdownMenuSeparator />
			<DropdownMenuItem
				variant="destructive"
				disabled={!actions.canManageInvitations}
				title={manageTitle}
				onClick={() => {
					actions.onRevoke(invitation);
				}}
			>
				Revoke invite
			</DropdownMenuItem>
		</RowMenu>
	);
}

const MONO = "font-mono text-[12px]";

function teamColumns(now: number, accessLoading: boolean, actions: RowActions): DataColumn<TeamRow>[] {
	return [
		{
			id: "person",
			header: "Person",
			size: 220,
			minSize: 140,
			cell: ({ row }) => <Person row={row.original} now={now} actions={actions} />,
		},
		{
			id: "status",
			header: "Status",
			size: 100,
			minSize: 90,
			cell: ({ row: { original: row } }) => {
				const expired = row.kind === "invitation" && tsMs(row.invitation.expiresAt) <= now;
				return (
					<Badge
						size="sm"
						tone={row.kind === "member" || expired ? "muted" : "info"}
						className={row.kind === "member" ? "text-fg2" : undefined}
					>
						{row.kind === "member" ? "Member" : expired ? "Expired" : "Invited"}
					</Badge>
				);
			},
		},
		{
			id: "role",
			header: "Role",
			size: 120,
			minSize: 90,
			cell: ({ row: { original: row } }) =>
				row.kind === "invitation" ? roleLabel(row.invitation.role) : <RoleCell member={row.member} actions={actions} />,
		},
		{
			id: "workspaces",
			header: "Workspaces",
			size: 160,
			minSize: 120,
			meta: { grow: true },
			cell: ({ row: { original: row } }) =>
				row.kind === "invitation" ? null : (
					<Workspaces member={row.member} accessLoading={accessLoading} actions={actions} />
				),
		},
		{
			id: "active",
			header: "Last active",
			size: 110,
			minSize: 90,
			meta: { cellClassName: cn(MONO, "text-fg2") },
			cell: ({ row: { original: row } }) => {
				if (row.kind === "invitation") return <Dash />;
				const last = row.member.member.lastLoginAt;
				return <span title="Last recorded sign-in">{last ? agoLabel(tsMs(last), now) : <Dash />}</span>;
			},
		},
		{
			id: "joined",
			header: "Joined / expires",
			size: 130,
			minSize: 90,
			meta: { cellClassName: cn(MONO, "text-fg3") },
			cell: ({ row }) => <Joined row={row.original} now={now} />,
		},
		{
			id: "menu",
			meta: { pinEnd: true, label: "Actions" },
			cell: ({ row: { original: row } }) =>
				row.kind === "invitation" ? (
					<InvitationMenu invitation={row.invitation} now={now} actions={actions} />
				) : (
					<MemberMenu member={row.member} actions={actions} />
				),
		},
	];
}

export function TeamTable({
	rows,
	now,
	isLoading,
	accessLoading,
	empty,
	actions,
}: {
	rows: TeamRow[];
	now: number;
	isLoading: boolean;
	accessLoading: boolean;
	empty: ReactNode;
	actions: RowActions;
}) {
	const table = useDataTable("team", {
		columns: teamColumns(now, accessLoading, actions),
		data: rows,
		getRowId: (row) =>
			row.kind === "invitation" ? `invitation:${row.invitation.id}` : `member:${row.member.member.user?.id ?? ""}`,
	});
	return <DataTable table={table} label="People" band loading={isLoading} empty={empty} />;
}
