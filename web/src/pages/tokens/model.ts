import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { EntityType, Scope } from "@gen/loco/token/v1/token_pb";
import type { EntityScope, Token } from "@gen/loco/token/v1/token_pb";

export type OwnerKey = "org" | "workspace" | "personal";
export type NodeKind = "org" | "ws" | "res" | "user" | "system";
export type Level = 1 | 2 | 3;

export const LEVELS: Level[] = [1, 2, 3];
export const LEVEL_LABEL: Record<Level, string> = { 1: "Read", 2: "Write", 3: "Admin" };
export const LEVEL_BADGE: Record<Level, string> = {
	1: "bg-bg3 text-fg2",
	2: "bg-info-bg text-info-fg",
	3: "bg-warn-bg text-warn-fg",
};
export const LEVEL_TEXT: Record<Level, string> = { 1: "text-fg2", 2: "text-info-fg", 3: "text-warn-fg" };
export const LEVEL_BAR: Record<Level, string> = { 1: "bg-fg3", 2: "bg-primary", 3: "bg-warn" };

export const ALLOWS: Record<NodeKind, string[]> = {
	org: [
		"View the organization, its workspaces and members",
		"Create workspaces and add members",
		"Delete the organization and remove members",
	],
	ws: [
		"List resources, environments and members",
		"Create resources and environments, add members",
		"Delete the workspace or environments, remove members",
	],
	res: [
		"View status, logs, events and deployments",
		"Deploy, scale, restart, edit variables and domains",
		"Delete the resource",
	],
	user: [
		"View your profile, organizations and workspaces",
		"Update your profile and create organizations",
		"Delete your account",
	],
	system: ["Read anything on the platform", "Change anything on the platform", "Administer the platform"],
};

export const EXPIRY_DAYS = [1, 7, 30] as const;
export type ExpiryDays = (typeof EXPIRY_DAYS)[number];

export const DAY_MS = 86_400_000;

export interface EntityNode {
	key: string;
	kind: NodeKind;
	entityType: EntityType;
	id: string;
	label: string;
	parent: string | null;
}

export interface EntityTree {
	nodes: Map<string, EntityNode>;
	order: EntityNode[];
	childCount: Map<string, number>;
}

export interface HeldScopes {
	scopes: Set<string>;
	system: Set<Level>;
}

export type Grants = Map<string, Level>;

export function nodeKey(entityType: EntityType, id: string): string {
	return `${entityType.toString()}:${id}`;
}

export function kindOf(entityType: EntityType): NodeKind {
	switch (entityType) {
		case EntityType.ORGANIZATION:
			return "org";
		case EntityType.WORKSPACE:
			return "ws";
		case EntityType.RESOURCE:
			return "res";
		case EntityType.USER:
			return "user";
		case EntityType.SYSTEM:
			return "system";
		case EntityType.UNSPECIFIED:
			return "system";
	}
}

export function kindLabel(kind: NodeKind): string {
	switch (kind) {
		case "org":
			return "Organization";
		case "ws":
			return "Workspace";
		case "res":
			return "Resource";
		case "user":
			return "User";
		case "system":
			return "System";
	}
}

export function scopeLevel(scope: Scope): Level | 0 {
	switch (scope) {
		case Scope.READ:
			return 1;
		case Scope.WRITE:
			return 2;
		case Scope.ADMIN:
			return 3;
		case Scope.UNSPECIFIED:
			return 0;
	}
}

export function levelScope(level: Level): Scope {
	switch (level) {
		case 1:
			return Scope.READ;
		case 2:
			return Scope.WRITE;
		case 3:
			return Scope.ADMIN;
	}
}

export function buildHeld(scopes: EntityScope[]): HeldScopes {
	const held: HeldScopes = { scopes: new Set(), system: new Set() };
	for (const s of scopes) {
		const level = scopeLevel(s.scope);
		if (level === 0) continue;
		if (s.entityType === EntityType.SYSTEM) {
			held.system.add(level);
			continue;
		}
		const key = nodeKey(s.entityType, s.entityId);
		held.scopes.add(`${key}#${level.toString()}`);
	}
	return held;
}

export function holds(held: HeldScopes, tree: EntityTree, key: string, level: Level): boolean {
	if (held.system.has(level)) return true;
	let cur: string | null = key;
	while (cur !== null) {
		if (held.scopes.has(`${cur}#${level.toString()}`)) return true;
		cur = tree.nodes.get(cur)?.parent ?? null;
	}
	return false;
}

export function heldLevel(held: HeldScopes, tree: EntityTree, key: string): Level | 0 {
	for (const level of [3, 2, 1] as const) {
		if (holds(held, tree, key, level)) return level;
	}
	return 0;
}

export function tokenGrants(token: Token): Grants {
	const grants: Grants = new Map();
	for (const s of token.scopes) {
		const level = scopeLevel(s.scope);
		if (level === 0) continue;
		const key = nodeKey(s.entityType, s.entityId);
		const prev = grants.get(key) ?? 0;
		if (level > prev) grants.set(key, level);
	}
	return grants;
}

export function grantsToScopes(items: { node: EntityNode; level: Level }[]): { entityType: EntityType; entityId: string; scope: Scope }[] {
	const out: { entityType: EntityType; entityId: string; scope: Scope }[] = [];
	for (const it of items) {
		for (const level of LEVELS) {
			if (level > it.level) break;
			out.push({ entityType: it.node.entityType, entityId: it.node.id, scope: levelScope(level) });
		}
	}
	return out;
}

export function resolveNode(tree: EntityTree, key: string): EntityNode {
	return tree.nodes.get(key) ?? { key, kind: "system", entityType: EntityType.UNSPECIFIED, id: key, label: key, parent: null };
}

export function withScopeEntities(tree: EntityTree, scopes: EntityScope[]): EntityTree {
	const nodes = new Map(tree.nodes);
	for (const s of scopes) {
		const key = nodeKey(s.entityType, s.entityId);
		if (nodes.has(key)) continue;
		const kind = kindOf(s.entityType);
		const label = kind === "system" ? "Platform" : s.entityId.slice(0, 8);
		nodes.set(key, { key, kind, entityType: s.entityType, id: s.entityId, label, parent: null });
	}
	return { nodes, order: tree.order, childCount: tree.childCount };
}

const KIND_ORDER: Record<NodeKind, number> = { system: 0, user: 1, org: 2, ws: 3, res: 4 };

export function sortKeys(tree: EntityTree, keys: string[]): string[] {
	const index = new Map(tree.order.map((n, i) => [n.key, i]));
	return keys.toSorted((a, b) => {
		const na = resolveNode(tree, a);
		const nb = resolveNode(tree, b);
		const kd = KIND_ORDER[na.kind] - KIND_ORDER[nb.kind];
		if (kd !== 0) return kd;
		return (index.get(a) ?? Number.MAX_SAFE_INTEGER) - (index.get(b) ?? Number.MAX_SAFE_INTEGER);
	});
}

export function summarize(tree: EntityTree, keys: string[]): string {
	const nodes = sortKeys(tree, keys).map((k) => resolveNode(tree, k));
	const parts: string[] = [];
	for (const n of nodes) {
		switch (n.kind) {
			case "system":
				parts.push("platform (system)");
				break;
			case "user":
				parts.push("your account");
				break;
			case "org":
				parts.push(`${n.label} (org)`);
				break;
			case "ws":
				parts.push(`${n.label} (workspace)`);
				break;
			case "res":
				break;
		}
	}
	const byWs = new Map<string, string[]>();
	for (const n of nodes) {
		if (n.kind !== "res") continue;
		const parentKey = n.parent ?? "";
		const list = byWs.get(parentKey) ?? [];
		list.push(n.label);
		byWs.set(parentKey, list);
	}
	for (const [ws, names] of byWs) {
		const wsLabel = tree.nodes.get(ws)?.label;
		if (names.length > 3) {
			const head = names.slice(0, 2).join(", ");
			const rest = (names.length - 2).toString();
			parts.push(wsLabel === undefined ? `${head} +${rest}` : `${head} +${rest} in ${wsLabel}`);
		} else {
			parts.push(names.join(", "));
		}
	}
	return parts.join(" · ");
}

export interface AccessLine {
	level: Level;
	names: string;
}

export function accessLines(tree: EntityTree, grants: Grants): AccessLine[] {
	const lines: AccessLine[] = [];
	for (const level of [3, 2, 1] as const) {
		const keys = [...grants.entries()].filter(([, v]) => v === level).map(([k]) => k);
		if (keys.length === 0) continue;
		lines.push({ level, names: summarize(tree, keys) });
	}
	return lines;
}

export type TokenStatus = "active" | "expiring" | "expired";

export function tsMillis(ts: Timestamp | undefined): number | null {
	if (!ts) return null;
	return Number(ts.seconds) * 1000;
}

export function tokenStatus(token: Token, now: number): TokenStatus {
	const exp = tsMillis(token.expiresAt);
	if (exp === null) return "active";
	if (exp < now) return "expired";
	if (exp - now < 7 * DAY_MS) return "expiring";
	return "active";
}

export function daysLeft(token: Token, now: number): number {
	const exp = tsMillis(token.expiresAt) ?? now;
	return Math.ceil((exp - now) / DAY_MS);
}

export function expiresLabel(token: Token, now: number): string {
	if (tokenStatus(token, now) === "expired") return "Expired";
	const left = daysLeft(token, now);
	return left === 1 ? "in 1 day" : `in ${left.toString()} days`;
}

export function relAgo(ms: number, now: number): string {
	const d = now - ms;
	if (d < 3_600_000) return `${Math.max(1, Math.round(d / 60_000)).toString()} min ago`;
	if (d < DAY_MS) return `${Math.round(d / 3_600_000).toString()}h ago`;
	const n = Math.round(d / DAY_MS);
	return n === 1 ? "yesterday" : `${n.toString()} days ago`;
}

export function fmtDay(ms: number): string {
	return new Date(ms).toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

export function lastUsedLabel(token: Token, now: number): string {
	const used = tsMillis(token.lastUsedAt);
	return used === null ? "Never" : relAgo(used, now);
}

export function matchesQuery(tree: EntityTree, token: Token, q: string): boolean {
	if (q === "") return true;
	if (token.name.toLowerCase().includes(q)) return true;
	for (const s of token.scopes) {
		const node = tree.nodes.get(nodeKey(s.entityType, s.entityId));
		if (node?.label.toLowerCase().includes(q) === true) return true;
	}
	return false;
}
