import { EntityType, Scope } from "@gen/loco/token/v1/token_pb";
import type { EntityScope, Token } from "@gen/loco/token/v1/token_pb";

import { scopeLevel } from "@/hooks/useMyScopes";
import { pluralize } from "@/lib/format";
import { agoLabel, DAY_MS, maybeTsMs, shortDurationLabel } from "@/lib/time";

export type NodeKind = "org" | "ws" | "res" | "user" | "system";
export type Level = 1 | 2 | 3;

export const OWNER_KEYS = ["org", "ws", "user"] as const;
export type OwnerKey = (typeof OWNER_KEYS)[number];
export const OWNER_TITLE: Record<OwnerKey, string> = {
	org: "Organization",
	ws: "Workspace",
	user: "Personal",
};

export const LEVELS: Level[] = [1, 2, 3];

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

export function defaultExpirySeconds(maxSeconds: bigint): number {
	return Math.min((30 * DAY_MS) / 1000, Number(maxSeconds));
}

const HOUR_MS = 3_600_000;
export const TOKEN_NAME_RE = /^[a-z0-9][a-z0-9-]+$/;

export function sanitizeTokenName(raw: string): string {
	return raw.toLowerCase().replace(/[^a-z0-9-]/g, "-");
}

export function customExpiryError(atMs: number, now: number, maxSeconds: bigint): string | null {
	if (Number.isNaN(atMs)) return "Pick a date and time.";
	if (atMs < now + HOUR_MS) return "Pick a time at least an hour from now.";
	const maxMs = Number(maxSeconds) * 1000;
	if (atMs > now + maxMs) return `Tokens can last at most ${pluralize(Math.floor(maxMs / DAY_MS), "day")}.`;
	return null;
}

export function expiryOptions(maxSeconds: bigint): number[] {
	const maximum = Number(maxSeconds);
	return [1, 7, 14, 30]
		.map((days) => (days * DAY_MS) / 1000)
		.filter((seconds) => seconds < maximum)
		.concat(maximum);
}

export interface EntityNode {
	key: string;
	kind: NodeKind;
	entityType: EntityType;
	id: string;
	label: string;
	parent: EntityNode | null;
	/** How many resources a workspace holds, when the caller can read it. */
	resources?: number;
}

/** The entities the page knows, by nodeKey. */
export type Entities = ReadonlyMap<string, EntityNode>;

export interface HeldScopes {
	scopes: Set<string>;
	system: Set<Level>;
}

export type Grants = Map<string, Level>;

export function nodeKey(entityType: EntityType, id: string): string {
	return `${entityType.toString()}:${id}`;
}

export function entityNode(
	entityType: EntityType,
	id: string,
	label: string,
	parent: EntityNode | null,
	resources?: number,
): EntityNode {
	const node: EntityNode = { key: nodeKey(entityType, id), kind: kindOf(entityType), entityType, id, label, parent };
	if (resources !== undefined) node.resources = resources;
	return node;
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

function levelScope(level: Level): Scope {
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

export function holds(held: HeldScopes, node: EntityNode, level: Level): boolean {
	if (held.system.has(level)) return true;
	for (let cur: EntityNode | null = node; cur !== null; cur = cur.parent) {
		if (held.scopes.has(`${cur.key}#${level.toString()}`)) return true;
	}
	return false;
}

export function heldLevel(held: HeldScopes, node: EntityNode): Level | 0 {
	for (const level of [3, 2, 1] as const) {
		if (holds(held, node, level)) return level;
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

export function grantsToScopes(
	items: { node: EntityNode; level: Level }[],
): { entityType: EntityType; entityId: string; scope: Scope }[] {
	const out: { entityType: EntityType; entityId: string; scope: Scope }[] = [];
	for (const it of items) {
		for (const level of LEVELS) {
			if (level > it.level) break;
			out.push({ entityType: it.node.entityType, entityId: it.node.id, scope: levelScope(level) });
		}
	}
	return out;
}

export function resolveNode(entities: Entities, key: string): EntityNode {
	return (
		entities.get(key) ?? {
			key,
			kind: "system",
			entityType: EntityType.UNSPECIFIED,
			id: key,
			label: key,
			parent: null,
		}
	);
}

/** Adds the scopes' entities the page could not name, labelled by a short ID. */
export function withScopeEntities(entities: Entities, scopes: EntityScope[]): Entities {
	const nodes = new Map(entities);
	for (const s of scopes) {
		const key = nodeKey(s.entityType, s.entityId);
		if (nodes.has(key)) continue;
		const label = s.entityType === EntityType.SYSTEM ? "Platform" : s.entityId.slice(0, 8);
		nodes.set(key, entityNode(s.entityType, s.entityId, label, null));
	}
	return nodes;
}

const KIND_ORDER: Record<NodeKind, number> = { system: 0, user: 1, org: 2, ws: 3, res: 4 };

/** Orders keys by kind; organizations keep the entities' order, which puts the active one first, and the rest sort by name. */
export function sortKeys(entities: Entities, keys: string[]): string[] {
	const index = new Map([...entities.keys()].map((k, i) => [k, i]));
	const at = (k: string) => index.get(k) ?? Number.MAX_SAFE_INTEGER;
	return keys.toSorted((a, b) => {
		const na = resolveNode(entities, a);
		const nb = resolveNode(entities, b);
		const kd = KIND_ORDER[na.kind] - KIND_ORDER[nb.kind];
		if (kd !== 0) return kd;
		return na.kind === "org" ? at(a) - at(b) : na.label.localeCompare(nb.label);
	});
}

export interface AccessChip {
	key: string;
	level: Level;
	node: EntityNode;
}

export interface Creator {
	name: string;
	avatarUrl: string;
}

export interface LevelGroup {
	level: Level;
	nodes: EntityNode[];
}

export function accessByLevel(chips: readonly AccessChip[]): LevelGroup[] {
	return ([3, 2, 1] as const).flatMap((level) => {
		const nodes = chips.filter((c) => c.level === level).map((c) => c.node);
		return nodes.length === 0 ? [] : [{ level, nodes }];
	});
}

export function accessChips(entities: Entities, token: Token): AccessChip[] {
	const grants = tokenGrants(token);
	return sortKeys(entities, [...grants.keys()])
		.map((key) => ({ key, level: grants.get(key) ?? 1, node: resolveNode(entities, key) }))
		.toSorted((a, b) => b.level - a.level);
}

const ALLOWS_SCOPE: Record<NodeKind, string> = {
	org: "",
	ws: " (selected workspaces)",
	res: " (selected resources)",
	user: "",
	system: "",
};

export function allowsFor(kinds: { kind: NodeKind; level: Level }[]): string[] {
	const max = new Map<NodeKind, Level>();
	for (const k of kinds) {
		if (k.level > (max.get(k.kind) ?? 0)) max.set(k.kind, k.level);
	}
	return [...max.entries()]
		.toSorted(([a], [b]) => KIND_ORDER[a] - KIND_ORDER[b])
		.flatMap(([kind, level]) => ALLOWS[kind].slice(0, level).map((line) => `${line}${ALLOWS_SCOPE[kind]}`));
}

export const EXPIRING_DAYS = 7;

export type TokenStatus = "active" | "expiring" | "expired";

export function tokenStatus(token: Token, now: number): TokenStatus {
	const exp = maybeTsMs(token.expiresAt);
	if (exp === undefined) return "active";
	if (exp < now) return "expired";
	if (exp - now <= EXPIRING_DAYS * DAY_MS) return "expiring";
	return "active";
}

export function expiresLabel(token: Token, now: number): string {
	const exp = maybeTsMs(token.expiresAt) ?? now;
	return exp < now ? `expired ${agoLabel(exp, now)}` : `in ${shortDurationLabel(exp - now)}`;
}

export function relAgo(ms: number, now: number): string {
	const d = now - ms;
	if (d < 3_600_000) return `${Math.max(1, Math.round(d / 60_000)).toString()} min ago`;
	if (d < DAY_MS) return `${Math.round(d / 3_600_000).toString()}h ago`;
	const n = Math.round(d / DAY_MS);
	return n === 1 ? "yesterday" : `${n.toString()} days ago`;
}

export function lastUsedLabel(token: Token, now: number): string {
	const used = maybeTsMs(token.lastUsedAt);
	return used === undefined ? "never" : agoLabel(used, now);
}

export function matchesQuery(entities: Entities, token: Token, q: string, creator = ""): boolean {
	if (q === "") return true;
	if (token.name.toLowerCase().includes(q) || creator.toLowerCase().includes(q)) return true;
	for (const s of token.scopes) {
		const node = entities.get(nodeKey(s.entityType, s.entityId));
		if (node?.label.toLowerCase().includes(q) === true) return true;
	}
	return false;
}
