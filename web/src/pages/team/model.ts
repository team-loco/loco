import { Code, ConnectError } from "@connectrpc/connect";
import { OrgPeopleView, OrgRole, type Invitation, type OrgMember, type OrgPerson } from "@gen/loco/org/v1/org_pb";

import type { AccessLevel } from "@/hooks/useMyScopes";
import { getErrorMessage } from "@/lib/error-handler";
import { shortDurationLabel } from "@/lib/time";

export type ScopeName = "read" | "write" | "admin";
export type Level = AccessLevel;
export type View = "all" | "members" | "invitations";
export type Role = "Admin" | "Member";

export const SCOPE_NAMES: ScopeName[] = ["read", "write", "admin"];
export const LEVELS: Level[] = [0, 1, 2, 3];
export const VIEWS: { key: View; label: string; view: OrgPeopleView }[] = [
	{ key: "all", label: "All", view: OrgPeopleView.UNSPECIFIED },
	{ key: "members", label: "Members", view: OrgPeopleView.MEMBERS },
	{ key: "invitations", label: "Invitations", view: OrgPeopleView.INVITATIONS },
];

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

export const ROLE_HINT: Record<Role, string> = {
	Admin: "Manage members and every workspace.",
	Member: "Only sees the workspaces you give them.",
};

export function roleLabel(role: OrgRole): Role {
	return role === OrgRole.ADMIN ? "Admin" : "Member";
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

export function untilLabel(ms: number, now: number): string {
	return ms <= now ? "expired" : `in ${shortDurationLabel(ms - now)}`;
}

/** The rows of one page of ListOrgPeople. */
export function teamRows(people: readonly OrgPerson[]): TeamRow[] {
	return people.flatMap((person): TeamRow[] => {
		if (person.person.case === "invitation") return [{ kind: "invitation", invitation: person.person.value }];
		if (person.person.case !== "member") return [];
		const workspaces = person.workspaces.map((w) => ({
			id: w.workspaceId,
			name: w.workspaceName,
			scopes: w.scopes.map(parseScopeName).filter((s): s is ScopeName => s !== null),
		}));
		return [{ kind: "member", member: { member: person.person.value, workspaces } }];
	});
}
