import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { EntityScopeSchema, EntityType, Scope } from "@gen/loco/token/v1/token_pb";
import { hasOrganizationMembership, holdsScope, levelOn, scopeLevel } from "./useMyScopes";

const ORG = "organization";

describe("organization membership", () => {
	test("accepts every granted organization capability and system capability", () => {
		for (const scope of [Scope.MEMBER, Scope.READ, Scope.WRITE, Scope.ADMIN]) {
			expect(
				hasOrganizationMembership(
					[create(EntityScopeSchema, { entityType: EntityType.ORGANIZATION, entityId: ORG, scope })],
					ORG,
				),
			).toBe(true);
			expect(
				hasOrganizationMembership([create(EntityScopeSchema, { entityType: EntityType.SYSTEM, scope })], ORG),
			).toBe(true);
		}
	});
	test("does not infer organization membership from narrower access or another organization", () => {
		for (const entityType of [EntityType.WORKSPACE, EntityType.RESOURCE, EntityType.USER]) {
			expect(
				hasOrganizationMembership([create(EntityScopeSchema, { entityType, entityId: ORG, scope: Scope.ADMIN })], ORG),
			).toBe(false);
		}
		expect(
			hasOrganizationMembership(
				[create(EntityScopeSchema, { entityType: EntityType.ORGANIZATION, entityId: "other", scope: Scope.ADMIN })],
				ORG,
			),
		).toBe(false);
		expect(
			hasOrganizationMembership(
				[create(EntityScopeSchema, { entityType: EntityType.ORGANIZATION, entityId: ORG })],
				ORG,
			),
		).toBe(false);
		expect(hasOrganizationMembership([], ORG)).toBe(false);
	});
});

describe("system scopes", () => {
	test("a system read scope reads every entity and unlocks no write or admin control", () => {
		const scopes = [create(EntityScopeSchema, { entityType: EntityType.SYSTEM, scope: Scope.READ })];
		for (const entityType of [EntityType.ORGANIZATION, EntityType.WORKSPACE, EntityType.RESOURCE]) {
			const level = levelOn(scopes, entityType, "any");
			expect(level).toBe(scopeLevel(Scope.READ));
			expect(level).toBeLessThan(scopeLevel(Scope.WRITE));
		}
	});
	test("a system admin scope gives write and admin level and holds every scope", () => {
		const scopes = [create(EntityScopeSchema, { entityType: EntityType.SYSTEM, scope: Scope.ADMIN })];
		for (const entityType of [EntityType.ORGANIZATION, EntityType.WORKSPACE, EntityType.RESOURCE]) {
			expect(levelOn(scopes, entityType, "any")).toBe(scopeLevel(Scope.ADMIN));
		}
		for (const scope of [Scope.READ, Scope.WRITE, Scope.ADMIN]) {
			expect(holdsScope(scopes, ORG, "ws", scope)).toBe(true);
		}
	});
	test("a system read scope holds read only", () => {
		const scopes = [create(EntityScopeSchema, { entityType: EntityType.SYSTEM, scope: Scope.READ })];
		expect(holdsScope(scopes, ORG, "ws", Scope.READ)).toBe(true);
		expect(holdsScope(scopes, ORG, "ws", Scope.WRITE)).toBe(false);
		expect(holdsScope(scopes, ORG, "ws", Scope.ADMIN)).toBe(false);
	});
});
