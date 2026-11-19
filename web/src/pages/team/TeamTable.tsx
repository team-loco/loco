import type { Invitation } from "@gen/loco/org/v1/org_pb";
import {
	CheckIcon,
	ChevronDownIcon,
	CopyIcon,
	LayersIcon,
	RotateCwIcon,
	Trash2Icon,
	UserCogIcon,
	UserMinusIcon,
} from "lucide-react";
import type { ReactNode } from "react";

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
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
} from "@/components/design/DropdownMenu";
import { LevelChip } from "@/components/design/LevelChip";
import { RowMenu } from "@/components/design/RowMenu";
import { Skeleton } from "@/components/design/Skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { formatMonthYear, shortAgoLabel, tsMs } from "@/lib/time";
import { userLabel } from "@/lib/user";

import { MemberAvatar } from "./MemberAvatar";
import { levelOf, ROLE_HINT, roleLabel, roleOf, untilLabel, type Member, type Role, type TeamRow } from "./model";

const SKELETON_ROWS = [0, 1, 2, 3];
const COLUMNS = 7;

export interface RowActions {
	meId: string;
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

function MemberRow({
	member,
	now,
	accessLoading,
	actions,
}: {
	member: Member;
	now: number;
	accessLoading: boolean;
	actions: RowActions;
}) {
	const pref = useTimePreferences();
	const user = member.member.user;
	const isMe = user?.id === actions.meId;
	const name = user === undefined ? "" : userLabel(user);
	const role = roleOf(member.member.scopes);
	const granted = member.workspaces.toSorted(
		(a, b) => levelOf(b.scopes) - levelOf(a.scopes) || a.name.localeCompare(b.name),
	);
	const first = granted[0];
	const openAccess = () => {
		actions.onAccess(member);
	};
	const roleLocked = isMe || !actions.canChangeRoles;
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
					Active
				</Badge>
			</TableCell>
			<TableCell>
				<DropdownMenu>
					<DropdownMenuTrigger
						render={
							<Button
								variant="ghost"
								size="xs"
								disabled={roleLocked}
								aria-label={`Change role for ${name}`}
								title={roleTitle(isMe, actions.canChangeRoles)}
								className="-ml-2 h-7 gap-1.5 border-transparent px-2 text-[13px] text-foreground hover:border-line2 hover:bg-background aria-expanded:border-line2 aria-expanded:bg-background disabled:opacity-100 disabled:hover:border-transparent disabled:hover:bg-transparent dark:hover:bg-background"
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
			</TableCell>
			<TableCell>
				{role === "Admin" && (
					<span title="All workspaces · Admin">
						<LevelChip level={3} name="All workspaces" nameFirst />
					</span>
				)}
				{role === "Member" && first === undefined && accessLoading && <Skeleton className="h-5 w-32" />}
				{role === "Member" && first === undefined && !accessLoading && (
					<Button variant="outline" size="xs" disabled={isMe} onClick={openAccess} className="text-fg3">
						No access
					</Button>
				)}
				{role === "Member" && first !== undefined && (
					<span className="flex min-w-0 items-center gap-1">
						<LevelChip level={levelOf(first.scopes)} name={first.name} nameFirst />
						{granted.length > 1 && (
							<Button
								variant="outline"
								size="xs"
								disabled={isMe}
								onClick={openAccess}
								aria-label={`All workspace access for ${name}`}
								className="shrink-0 font-mono text-[11.5px] text-fg2"
							>
								+{(granted.length - 1).toLocaleString()} more
							</Button>
						)}
					</span>
				)}
			</TableCell>
			<TableCell className="font-mono text-[12px] text-fg2" title="Last recorded sign-in">
				{member.member.lastLoginAt ? shortAgoLabel(tsMs(member.member.lastLoginAt), now) : <Dash />}
			</TableCell>
			<TableCell className="font-mono text-[12px] text-fg3">
				{member.member.joinedAt ? formatMonthYear(tsMs(member.member.joinedAt), pref) : <Dash />}
			</TableCell>
			<TableCell className="text-right">
				{!isMe && (
					<RowMenu label={`Actions for ${name}`} title={name}>
						<DropdownMenuGroup>
							<DropdownMenuSub>
								<DropdownMenuSubTrigger disabled={!actions.canChangeRoles}>
									<UserCogIcon />
									Change role
								</DropdownMenuSubTrigger>
								<DropdownMenuSubContent className="w-[260px]">
									<RoleItems
										role={role}
										onPick={(next) => {
											actions.onRole(member, next);
										}}
									/>
								</DropdownMenuSubContent>
							</DropdownMenuSub>
							{role === "Member" && (
								<DropdownMenuItem onClick={openAccess}>
									<LayersIcon />
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
							<UserMinusIcon />
							Remove from {actions.orgName}
						</DropdownMenuItem>
					</RowMenu>
				)}
			</TableCell>
		</TableRow>
	);
}

function InvitationRow({ invitation, now, actions }: { invitation: Invitation; now: number; actions: RowActions }) {
	const inviter = invitation.invitedBy;
	const sent = shortAgoLabel(tsMs(invitation.createdAt), now);
	const expired = tsMs(invitation.expiresAt) <= now;
	const expires = tsMs(invitation.expiresAt);
	const manageTitle = actions.canManageInvitations ? undefined : "Only organization admins can change invitations";
	return (
		<TableRow>
			<TableCell>
				<span className="flex min-w-0 items-center gap-2.5">
					<MemberAvatar name="" email={invitation.email} avatarUrl="" className="size-[30px] shrink-0" />
					<span className="flex min-w-0 flex-col gap-px">
						<span className="truncate text-[13.5px] font-medium">{invitation.email}</span>
						<span className="truncate font-mono text-[11.5px] text-fg3">
							{inviter === undefined
								? `invited ${sent}`
								: `invited by ${inviter.email || userLabel(inviter)} · ${sent}`}
						</span>
					</span>
				</span>
			</TableCell>
			<TableCell className="whitespace-normal">
				<span className="flex flex-col items-start gap-1">
					<Badge size="sm" tone={expired ? "muted" : "info"}>
						{expired ? "Expired" : "Invited"}
					</Badge>
				</span>
			</TableCell>
			<TableCell>{roleLabel(invitation.role)}</TableCell>
			<TableCell />
			<TableCell className="font-mono text-[12px]">
				<Dash />
			</TableCell>
			<TableCell className="font-mono text-[12px] text-fg3">
				{expired ? `expired ${shortAgoLabel(expires, now)}` : `expires ${untilLabel(expires, now)}`}
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
					<TableHead className="w-[22%]">Person</TableHead>
					<TableHead className="w-[130px]">Status</TableHead>
					<TableHead className="w-[140px]">Role</TableHead>
					<TableHead>Workspaces</TableHead>
					<TableHead className="w-[120px]">Last active</TableHead>
					<TableHead className="w-[120px]">Joined / expires</TableHead>
					<TableHead className="w-10" />
				</TableRow>
			</TableHeader>
			<TableBody>
				{isLoading &&
					SKELETON_ROWS.map((i) => (
						<TableRow key={i}>
							<TableCell>
								<span className="flex items-center gap-2.5">
									<Skeleton className="size-[30px] rounded-full" />
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
								now={now}
								accessLoading={accessLoading}
								actions={actions}
							/>
						),
					)}
			</TableBody>
		</Table>
	);
}
