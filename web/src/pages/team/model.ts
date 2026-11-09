import { Code, ConnectError } from "@connectrpc/connect";
import { InvitationStatus, type Invitation, type OrgMember } from "@gen/loco/org/v1/org_pb";

import type { BadgeTone } from "@/components/design/Badge";
import { getErrorMessage } from "@/lib/error-handler";
import { DAY_MS, tsMs } from "@/lib/time";

export type ScopeName = "read" | "write" | "admin";
export type Level = 0 | 1 | 2 | 3;
export type View = "all" | "members" | "invitations";
export type Role = "Admin" | "Member";

export const SCOPE_NAMES: ScopeName[] = ["read", "write", "admin"];
export const LEVELS: Level[] = [0, 1, 2, 3];
export const LEVEL_LABEL: Record<Level, string> = { 0: "None", 1: "Read", 2: "Write", 3: "Admin" };
export const LEVEL_TEXT: Record<Level, string> = { 0: "text-fg3", 1: "text-fg2", 2: "text-info-fg", 3: "text-warn-fg" };
export const VIEWS: { key: View; label: string }[] = [
	{ key: "all", label: "All" },
	{ key: "members", label: "Members" },
	{ key: "invitations", label: "Invitations" },
];

const HOUR_MS = 3_600_000;
const EMAIL_SEPARATORS = /[\s,;]+/;

export interface WorkspaceAccess {
	id: string;
	name: string;
	scopes: ScopeName[];
}

export interface Member {
	member: OrgMember;
	workspaces: WorkspaceAccess[];
}

export type TeamRow = { kind: "invitation"; invitation: Invitation } | { kind: "member"; member: Member };

export function parseScopeName(raw: string): ScopeName | null {
	switch (raw) {
		case "read":
			return "read";
		case "write":
			return "write";
		case "admin":
			return "admin";
		default:
			return null;
	}
}

export function roleOf(orgScopes: readonly string[]): Role {
	return orgScopes.includes("admin") ? "Admin" : "Member";
}

export function levelOf(scopes: readonly ScopeName[]): Level {
	if (scopes.includes("admin")) return 3;
	if (scopes.includes("write")) return 2;
	if (scopes.includes("read")) return 1;
	return 0;
}

export function scopesFor(level: Level): ScopeName[] {
	return SCOPE_NAMES.slice(0, level);
}

export interface LevelPlan {
	revokeAll: boolean;
	grant: ScopeName[];
}

export function levelPlan(current: readonly ScopeName[], level: Level): LevelPlan | null {
	if (levelOf(current) === level) return null;
	const next = scopesFor(level);
	const added = next.filter((s) => !current.includes(s));
	const removed = current.filter((s) => !next.includes(s));
	if (removed.length === 0) return { revokeAll: false, grant: added };
	return { revokeAll: true, grant: next };
}

export function levelBlocked(
	current: readonly ScopeName[],
	level: Level,
	holds: (scope: ScopeName) => boolean,
): string | null {
	const plan = levelPlan(current, level);
	if (plan === null) return null;
	if (plan.revokeAll && !holds("admin")) return "Lowering access needs admin on this workspace";
	if (plan.grant.length > 0 && !holds("write")) return "Granting access needs write on this workspace";
	const missing = plan.grant.find((s) => !holds(s));
	if (missing !== undefined) return `You don't hold ${missing} on this workspace`;
	return null;
}

export interface EmailChip {
	value: string;
	valid: boolean;
}

export function isPlainEmail(value: string): boolean {
	const at = value.indexOf("@");
	return at > 0 && at === value.lastIndexOf("@") && at < value.length - 1;
}

export function parseEmails(text: string): EmailChip[] {
	const seen = new Set<string>();
	const chips: EmailChip[] = [];
	for (const part of text.split(EMAIL_SEPARATORS)) {
		if (part === "") continue;
		const key = part.toLowerCase();
		if (seen.has(key)) continue;
		seen.add(key);
		chips.push({ value: part, valid: isPlainEmail(part) });
	}
	return chips;
}

export type InviteOutcome =
	| { kind: "invited"; email: string; invitation: Invitation }
	| { kind: "exists"; email: string; message: string }
	| { kind: "failed"; email: string; message: string };

export function inviteFailure(email: string, err: unknown): InviteOutcome {
	const message = getErrorMessage(err, "Failed to send the invitation");
	if (err instanceof ConnectError && err.code === Code.AlreadyExists) return { kind: "exists", email, message };
	return { kind: "failed", email, message };
}

export function invitationBadge(status: InvitationStatus): { label: string; tone: BadgeTone } {
	switch (status) {
		case InvitationStatus.EMAILED:
			return { label: "Invite sent", tone: "info" };
		case InvitationStatus.FAILED:
			return { label: "Delivery failed", tone: "bad" };
		case InvitationStatus.NO_TRANSPORT:
			return { label: "Not emailed", tone: "neutral" };
		case InvitationStatus.EXPIRED:
			return { label: "Expired", tone: "muted" };
		case InvitationStatus.UNSPECIFIED:
			return { label: "Unknown", tone: "muted" };
	}
}

export function needsSharedLink(status: InvitationStatus): boolean {
	return status === InvitationStatus.NO_TRANSPORT || status === InvitationStatus.FAILED;
}

export function untilLabel(ms: number, now: number): string {
	const left = ms - now;
	if (left <= 0) return "expired";
	if (left < DAY_MS) {
		const hours = Math.max(1, Math.round(left / HOUR_MS));
		return `in ${hours.toString()}h`;
	}
	const days = Math.round(left / DAY_MS);
	return days === 1 ? "in 1 day" : `in ${days.toString()} days`;
}

function displayName(m: Member): string {
	const user = m.member.user;
	return user === undefined ? "" : user.name || user.email;
}

function matches(row: TeamRow, q: string): boolean {
	if (q === "") return true;
	if (row.kind === "invitation") return row.invitation.email.toLowerCase().includes(q);
	const user = row.member.member.user;
	return `${user?.name ?? ""} ${user?.email ?? ""}`.toLowerCase().includes(q);
}

export function buildRows(
	members: readonly Member[],
	invitations: readonly Invitation[],
	view: View,
	query: string,
	meId: string,
): TeamRow[] {
	const q = query.trim().toLowerCase();
	const invitationRows: TeamRow[] =
		view === "members"
			? []
			: invitations
					.toSorted((a, b) => tsMs(b.createdAt) - tsMs(a.createdAt))
					.map((invitation) => ({ kind: "invitation", invitation }));
	const memberRows: TeamRow[] =
		view === "invitations"
			? []
			: members
					.toSorted((a, b) => {
						const aMe = a.member.user?.id === meId ? 0 : 1;
						const bMe = b.member.user?.id === meId ? 0 : 1;
						return aMe - bMe || displayName(a).localeCompare(displayName(b));
					})
					.map((member) => ({ kind: "member", member }));
	return [...invitationRows, ...memberRows].filter((row) => matches(row, q));
}
