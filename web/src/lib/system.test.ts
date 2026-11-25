import { describe, expect, test } from "bun:test";
import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { ClusterSchema } from "@gen/loco/cluster/v1/cluster_pb";
import { EntityScopeSchema, EntityType, Scope } from "@gen/loco/token/v1/token_pb";

import { agentBehind, clusterState, inSystem, regionGroups, systemAccess, workerInstallCommand } from "./system";

const NOW = 1_700_000_000_000;

function cluster(fields: MessageInitShape<typeof ClusterSchema> = {}) {
	return create(ClusterSchema, {
		id: "c",
		name: "c",
		region: "us-ca-1",
		isActive: true,
		healthStatus: "healthy",
		lastHeartbeat: timestampFromMs(NOW - 10_000),
		...fields,
	});
}

describe("clusterState", () => {
	test("maps the reported health while heartbeats are fresh", () => {
		expect(clusterState(cluster(), NOW)).toBe("healthy");
		expect(clusterState(cluster({ healthStatus: "degraded" }), NOW)).toBe("degraded");
		expect(clusterState(cluster({ healthStatus: "unhealthy" }), NOW)).toBe("unhealthy");
	});

	test("a stale heartbeat is not reporting and none is never reported", () => {
		expect(clusterState(cluster({ lastHeartbeat: timestampFromMs(NOW - 6 * 60_000) }), NOW)).toBe("not-reporting");
		expect(clusterState(cluster({ lastHeartbeat: undefined }), NOW)).toBe("never-reported");
	});

	test("paused and removed come before health", () => {
		expect(clusterState(cluster({ isActive: false }), NOW)).toBe("paused");
		expect(clusterState(cluster({ isActive: false, removedAt: timestampFromMs(NOW) }), NOW)).toBe("removed");
	});
});

describe("regionGroups", () => {
	test("puts the region with the worst cluster first", () => {
		const groups = regionGroups(
			[
				cluster({ id: "a", name: "a", region: "de-he-1" }),
				cluster({ id: "b", name: "b", region: "us-ca-1", healthStatus: "unhealthy" }),
				cluster({ id: "c", name: "c", region: "us-ca-1" }),
			],
			NOW,
		);
		expect(groups.map((g) => g.region)).toEqual(["us-ca-1", "de-he-1"]);
		expect(groups[0]?.clusters.map((c) => c.cluster.name)).toEqual(["b", "c"]);
	});
});

describe("agentBehind", () => {
	test("compares release versions", () => {
		expect(agentBehind("v0.0.218", "v0.0.217")).toBe(true);
		expect(agentBehind("v0.0.218", "v0.0.218")).toBe(false);
		expect(agentBehind("v0.1.0", "0.0.999")).toBe(true);
		expect(agentBehind("v0.0.218", "v0.0.219")).toBe(false);
	});

	test("is false when a version is not a release", () => {
		expect(agentBehind("(devel)", "v0.0.1")).toBe(false);
		expect(agentBehind("v0.0.2", "sha-abc")).toBe(false);
		expect(agentBehind("v0.0.2", undefined)).toBe(false);
	});
});

describe("systemAccess", () => {
	const scope = (entityType: EntityType, s: Scope) => create(EntityScopeSchema, { entityType, scope: s });

	test("reads the scopes on the system entity", () => {
		expect(systemAccess([scope(EntityType.SYSTEM, Scope.ADMIN)])).toBe("admin");
		expect(systemAccess([scope(EntityType.SYSTEM, Scope.READ)])).toBe("read");
		expect(systemAccess([scope(EntityType.ORGANIZATION, Scope.ADMIN)])).toBe("none");
		expect(systemAccess([])).toBe("none");
	});
});

test("inSystem matches /system and its children only", () => {
	expect(inSystem("/system")).toBe(true);
	expect(inSystem("/system/clusters/x")).toBe(true);
	expect(inSystem("/systems")).toBe(false);
});

test("workerInstallCommand names the secret the chart reads", () => {
	const [secret, helm] = workerInstallCommand("us-ca-1", "tok").split("\n");
	expect(secret).toContain("--from-literal=join-token=tok");
	expect(helm).toContain("--set global.region=us-ca-1");
	expect(helm).toContain("--set agent.joinToken.existingSecret=loco-agent-join-token");
});
