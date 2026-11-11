import type { Invitation } from "@gen/loco/org/v1/org_pb";
import { InvitationStatus } from "@gen/loco/org/v1/org_pb";
import { ChevronDownIcon, CopyIcon, LayersIcon, RotateCwIcon, Trash2Icon, UserMinusIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuTrigger,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuSeparator,
} from "@/components/design/DropdownMenu";
import { LevelChip } from "@/components/design/LevelChip";
import { RowMenu } from "@/components/design/RowMenu";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { agoLabel, tsMs } from "@/lib/time";
import { userLabel } from "@/lib/user";
import { cn } from "@/lib/utils";

import { MemberAvatar } from "./MemberAvatar";
import { invitationBadge, levelOf, roleOf, untilLabel, type Member, type Role, type TeamRow } from "./model";

const SKELETON_ROWS = [0, 1, 2, 3];
const COLUMNS = 7;

export interface RowActions {
	meId: string;
	orgName: string;
	canManageInvitations: boolean;
	onAccess: (member: Member) => void;
	onRole: (member: Member, role: Role) => void;
	onCopyLink: (invitation: Invitation) => void;
	onResend: (invitation: Invitation) => void;
	onRevoke: (invitation: Invitation) => void;
}

function Dash() {
	return <span className="text-fg4">—</span>;
}

function MemberRow({
	member,
	accessLoading,
	actions,
}: {
	member: Member;
	accessLoading: boolean;
	actions: RowActions;
}) {
	const user = member.member.user;
	const isMe = user?.id === actions.meId;
	const name = user === undefined ? "" : userLabel(user);
	const role = roleOf(member.member.scopes);
	return (
		<TableRow>
			<TableCell>
				<span className="flex min-w-0 items-center gap-2.5">
					<MemberAvatar
						name={user?.name ?? ""}
						email={user?.email ?? ""}
						avatarUrl={user?.avatarUrl ?? ""}
						className="size-[30px] shrink-0"
					/>
					<span className="flex min-w-0 flex-col gap-px">
						<span className="flex items-center gap-2 text-[13.5px] font-medium">
							<span className="truncate">{name}</span>
							{isMe && <Badge tone="pill">you</Badge>}
						</span>
						<span className="truncate font-mono text-[11.5px] text-fg3">{user?.email}</span>
					</span>
				</span>
			</TableCell>
			<TableCell>
				<Badge size="sm" tone="muted">
					Member
				</Badge>
			</TableCell>
			<TableCell>
				<DropdownMenu>
					<DropdownMenuTrigger
						render={
							<Button
								variant="ghost"
								size="xs"
								disabled={!actions.canManageInvitations}
								aria-label={`Change role for ${name}`}
								title={actions.canManageInvitations ? undefined : "Only organization admins can change roles"}
								className="-ml-2 gap-1 text-[13px] text-foreground disabled:opacity-100"
							/>
						}
					>
						{role}
						<ChevronDownIcon className="text-fg4" />
					</DropdownMenuTrigger>
					<DropdownMenuContent align="start">
						<DropdownMenuGroup>
							{(["Admin", "Member"] as const).map((next) => (
								<DropdownMenuItem
									key={next}
									disabled={next === role}
									onClick={() => {
										actions.onRole(member, next);
									}}
								>
									{next}
								</DropdownMenuItem>
							))}
						</DropdownMenuGroup>
					</DropdownMenuContent>
				</DropdownMenu>
			</TableCell>
			<TableCell className="whitespace-normal">
				{member.workspaces.length === 0 && accessLoading && <Skeleton className="h-5 w-32" />}
				{member.workspaces.length === 0 && !accessLoading && <Dash />}
				{member.workspaces.length > 0 && (
					<span className="flex min-w-0 flex-wrap gap-1">
						{member.workspaces.map((w) => {
							const level = levelOf(w.scopes);
							return <LevelChip key={w.id} level={level} name={w.name} nameFirst />;
						})}
					</span>
				)}
			</TableCell>
			<TableCell>
				<Dash />
			</TableCell>
			<TableCell>
				<Dash />
			</TableCell>
			<TableCell className="text-right">
				{!isMe && (
					<RowMenu label={`Actions for ${name}`} title={name}>
						<DropdownMenuGroup>
							<DropdownMenuItem
								onClick={() => {
									actions.onAccess(member);
								}}
							>
								<LayersIcon />
								Workspace access
							</DropdownMenuItem>
						</DropdownMenuGroup>
						<DropdownMenuSeparator />
						<DropdownMenuItem variant="destructive" disabled>
							<UserMinusIcon />
							<span className="flex-1">Remove from {actions.orgName}</span>
							<SoonTag />
						</DropdownMenuItem>
					</RowMenu>
				)}
			</TableCell>
		</TableRow>
	);
}

function InvitationRow({ invitation, now, actions }: { invitation: Invitation; now: number; actions: RowActions }) {
	const badge = invitationBadge(invitation.status);
	const inviter = invitation.invitedBy;
	const sent = agoLabel(tsMs(invitation.createdAt), now);
	const expired = invitation.status === InvitationStatus.EXPIRED;
	const expires = tsMs(invitation.expiresAt);
	const manageTitle = actions.canManageInvitations ? undefined : "Only organization admins can change invitations";
	return (
		<TableRow>
			<TableCell>
				<span className="flex min-w-0 items-center gap-2.5">
					<MemberAvatar name="" email={invitation.email} avatarUrl="" className="size-[30px] shrink-0" />
					<span className="flex min-w-0 flex-col gap-px">
						<span className="truncate text-[13.5px] font-medium">{invitation.email}</span>
						<span className="truncate text-[11.5px] text-fg3">
							{inviter === undefined ? `Invited ${sent}` : `Invited by ${userLabel(inviter)} · ${sent}`}
						</span>
					</span>
				</span>
			</TableCell>
			<TableCell className="whitespace-normal">
				<span className="flex flex-col items-start gap-1">
					<Badge size="sm" tone={badge.tone}>
						{badge.label}
					</Badge>
					{invitation.status === InvitationStatus.FAILED && invitation.deliveryError !== "" && (
						<span className="line-clamp-2 text-[11.5px] text-bad-fg" title={invitation.deliveryError}>
							{invitation.deliveryError}
						</span>
					)}
				</span>
			</TableCell>
			<TableCell className="text-fg3">Member</TableCell>
			<TableCell>
				<Dash />
			</TableCell>
			<TableCell>
				<Dash />
			</TableCell>
			<TableCell className={cn("font-mono text-[11.5px]", expired ? "text-fg3" : "text-fg2")}>
				{expired ? `expired ${agoLabel(expires, now)}` : `expires ${untilLabel(expires, now)}`}
			</TableCell>
			<TableCell className="text-right">
				<RowMenu label={`Actions for the invitation to ${invitation.email}`} title={invitation.email}>
					<DropdownMenuGroup>
						<DropdownMenuItem
							onClick={() => {
								actions.onCopyLink(invitation);
							}}
						>
							<CopyIcon />
							Copy invite link
						</DropdownMenuItem>
						<DropdownMenuItem
							disabled={!actions.canManageInvitations}
							title={manageTitle}
							onClick={() => {
								actions.onResend(invitation);
							}}
						>
							<RotateCwIcon />
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
						<Trash2Icon />
						Revoke invite
					</DropdownMenuItem>
				</RowMenu>
			</TableCell>
		</TableRow>
	);
}

function SoonHead({ children, className }: { children: ReactNode; className: string }) {
	return (
		<TableHead className={className}>
			<span className="flex items-center gap-1.5">
				{children}
				<SoonTag />
			</span>
		</TableHead>
	);
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
	return (
		<Table className="min-w-[880px] table-fixed">
			<TableHeader>
				<TableRow>
					<TableHead className="w-[23%]">Person</TableHead>
					<TableHead className="w-[124px]">Status</TableHead>
					<TableHead className="w-[116px]">Role</TableHead>
					<TableHead>Workspaces</TableHead>
					<SoonHead className="w-[104px]">Last active</SoonHead>
					<SoonHead className="w-[140px]">Joined</SoonHead>
					<TableHead className="w-10" />
				</TableRow>
			</TableHeader>
			<TableBody>
				{isLoading &&
					SKELETON_ROWS.map((i) => (
						<TableRow key={i}>
							<TableCell>
								<span className="flex items-center gap-2.5">
									<Skeleton className="size-[30px] rounded-lg" />
									<Skeleton className="h-3.5 w-36" />
								</span>
							</TableCell>
							<TableCell>
								<Skeleton className="h-4 w-16" />
							</TableCell>
							<TableCell>
								<Skeleton className="h-3.5 w-14" />
							</TableCell>
							<TableCell>
								<Skeleton className="h-5 w-40" />
							</TableCell>
							<TableCell />
							<TableCell />
							<TableCell />
						</TableRow>
					))}
				{!isLoading && rows.length === 0 && (
					<TableRow>
						<TableCell colSpan={COLUMNS} className="h-auto p-0 whitespace-normal">
							{empty}
						</TableCell>
					</TableRow>
				)}
				{!isLoading &&
					rows.map((row) =>
						row.kind === "invitation" ? (
							<InvitationRow key={row.invitation.id} invitation={row.invitation} now={now} actions={actions} />
						) : (
							<MemberRow
								key={row.member.member.user?.id}
								member={row.member}
								accessLoading={accessLoading}
								actions={actions}
							/>
						),
					)}
			</TableBody>
		</Table>
	);
}
