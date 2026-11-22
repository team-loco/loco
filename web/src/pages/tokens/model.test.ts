import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { EntityScopeSchema, EntityType, Scope, TokenSchema } from "@gen/loco/token/v1/token_pb";

import { DAY_MS } from "@/lib/time";

import {
	allowsFor,
	buildHeld,
	chipName,
	collapsedNames,
	customExpiryError,
	defaultExpirySeconds,
	expiryOptions,
	entityNode,
	expiresLabel,
	heldLevel,
	sanitizeTokenName,
	sortKeys,
	TOKEN_NAME_RE,
	tokenStatus,
} from "./model";

const NOW = Date.UTC(2026, 10, 8);

function expiringIn(ms: number) {
	return create(TokenSchema, { name: "ci", expiresAt: timestampFromMs(NOW + ms) });
}

describe("token expiry", () => {
	test("warns from seven days out and errors once expired", () => {
		expect(tokenStatus(expiringIn(7 * DAY_MS + 1), NOW)).toBe("active");
		expect(tokenStatus(expiringIn(7 * DAY_MS), NOW)).toBe("expiring");
		expect(tokenStatus(expiringIn(-1), NOW)).toBe("expired");
	});

	test("labels an expired token with how long ago it expired", () => {
		expect(expiresLabel(expiringIn(-3 * DAY_MS), NOW)).toBe("expired 3d ago");
		expect(expiresLabel(expiringIn(23 * DAY_MS), NOW)).toBe("in 23d");
	});
});

describe("allows list", () => {
	test("lists each kind up to its highest granted level", () => {
		expect(
			allowsFor([
				{ kind: "res", level: 1 },
				{ kind: "res", level: 2 },
				{ kind: "ws", level: 1 },
			]),
		).toEqual([
			"List resources, environments and members (selected workspaces)",
			"View status, logs, events and deployments (selected resources)",
			"Deploy, scale, restart, edit variables and domains (selected resources)",
		]);
	});
});

describe("token expiry options", () => {
	test("includes the configured maximum and only shorter presets", () => {
		const day = DAY_MS / 1000;
		expect(expiryOptions(BigInt(365 * day))).toEqual([day, 7 * day, 14 * day, 30 * day, 365 * day]);
		expect(expiryOptions(BigInt(2 * day))).toEqual([day, 2 * day]);
		expect(expiryOptions(BigInt(30 * day))).toEqual([day, 7 * day, 14 * day, 30 * day]);
		expect(expiryOptions(90n)).toEqual([90]);
	});
});

test("default token expiry is thirty days capped at the configured maximum", () => {
	const day = DAY_MS / 1000;
	expect(defaultExpirySeconds(BigInt(365 * day))).toBe(30 * day);
	expect(defaultExpirySeconds(BigInt(30 * day))).toBe(30 * day);
	expect(defaultExpirySeconds(BigInt(2 * day))).toBe(2 * day);
	expect(defaultExpirySeconds(90n)).toBe(90);
});

describe("custom token expiry", () => {
	const max = BigInt((30 * DAY_MS) / 1000);
	test("needs at least an hour and at most the configured maximum", () => {
		expect(customExpiryError(NOW + 30 * 60_000, NOW, max)).toBe("Pick a time at least an hour from now.");
		expect(customExpiryError(NOW + 31 * DAY_MS, NOW, max)).toBe("Tokens can last at most 30 days.");
		expect(customExpiryError(NOW + 3 * DAY_MS, NOW, max)).toBeNull();
		expect(customExpiryError(Number.NaN, NOW, max)).toBe("Pick a date and time.");
	});
});

test("token names keep only lowercase letters, digits and dashes", () => {
	expect(sanitizeTokenName("CI Deploy_1")).toBe("ci-deploy-1");
	expect(TOKEN_NAME_RE.test("ci-deploy")).toBe(true);
	expect(TOKEN_NAME_RE.test("-ci")).toBe(false);
	expect(TOKEN_NAME_RE.test("c")).toBe(false);
});

describe("held access", () => {
	const org = entityNode(EntityType.ORGANIZATION, "org", "acme", null);
	const ws = entityNode(EntityType.WORKSPACE, "ws", "shared", org);
	const res = entityNode(EntityType.RESOURCE, "res", "web", ws);
	const other = entityNode(EntityType.RESOURCE, "other", "api", entityNode(EntityType.WORKSPACE, "ws2", "x", null));
	const scope = (entityType: EntityType, entityId: string, s: Scope) =>
		create(EntityScopeSchema, { entityType, entityId, scope: s });

	test("a resource inherits the highest level held on its workspace or organization", () => {
		const held = buildHeld([
			scope(EntityType.ORGANIZATION, "org", Scope.READ),
			scope(EntityType.WORKSPACE, "ws", Scope.WRITE),
		]);
		expect(heldLevel(held, res)).toBe(2);
		expect(heldLevel(held, org)).toBe(1);
		expect(heldLevel(held, other)).toBe(0);
	});

	test("access chips keep the organizations' order and sort other kinds by name", () => {
		const beta = entityNode(EntityType.ORGANIZATION, "beta", "beta", null);
		const entities = new Map([org, beta, other, res, ws].map((n) => [n.key, n]));
		const keys = [res.key, beta.key, ws.key, other.key, org.key];
		expect(sortKeys(entities, keys)).toEqual([org.key, beta.key, ws.key, other.key, res.key]);
	});

	test("a system scope covers every entity", () => {
		expect(heldLevel(buildHeld([scope(EntityType.SYSTEM, "", Scope.ADMIN)]), other)).toBe(3);
	});
});

test("access chips name orgs and workspaces and collapse past three names", () => {
	const org = entityNode(EntityType.ORGANIZATION, "org", "acme", null);
	const ws = entityNode(EntityType.WORKSPACE, "ws", "shared", org);
	const res = entityNode(EntityType.RESOURCE, "res", "web", ws);
	expect([org, ws, res].map(chipName)).toEqual(["acme org", "shared workspace", "web"]);
	expect(collapsedNames(["a", "b", "c"])).toBe("a, b, c");
	expect(collapsedNames(["a", "b", "c", "d", "e"])).toBe("a, b, c +2");
});
