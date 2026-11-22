import { create } from "@bufbuild/protobuf";
import { DeploymentStatus, DeploymentSchema, DeploymentSpecSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { skipToken } from "@connectrpc/connect-query";
import { describe, expect, test } from "bun:test";
import { EnvironmentStatusSchema, RegionStatusSchema, ResourceSchema } from "@gen/loco/resource/v1/resource_pb";
import { ReplicaSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { SecretSchema, SecretUserSchema } from "@gen/loco/secret/v1/secret_pb";

import { isWorker } from "@/lib/resource-regions";

import { msToTimestamp } from "@/lib/time";

import {
	buildRegions,
	changedSecrets,
	deploymentsRequest,
	focusRegion,
	metricsPending,
	previousDeploymentId,
	regionDeployment,
	statusEnvironmentId,
	tookMs,
	type RegionView,
} from "./model";

const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const PRODUCTION_ID = "0199d1c2-0000-7000-8000-000000000002";

describe("deploymentsRequest", () => {
	test("asks for the newest page of the active environment's deployments", () => {
		expect(deploymentsRequest(RESOURCE_ID, PRODUCTION_ID)).toEqual({
			resourceId: RESOURCE_ID,
			environmentId: PRODUCTION_ID,
			pageSize: 10,
		});
	});

	test("waits for an environment instead of listing every environment", () => {
		expect(deploymentsRequest(RESOURCE_ID, undefined)).toBe(skipToken);
	});

	test("waits for a resource id", () => {
		expect(deploymentsRequest("", PRODUCTION_ID)).toBe(skipToken);
	});
});

describe("buildRegions", () => {
	test("keeps retired releases available without a current deployment", () => {
		const history = [3, 1, 2].map((day) =>
			create(DeploymentSchema, {
				id: `release-${day.toString()}`,
				placements: [{ region: "us-va-1" }],
				status: DeploymentStatus.SUPERSEDED,
				createdAt: msToTimestamp(day * 86_400_000),
				spec: create(DeploymentSpecSchema),
			}),
		);
		const [region] = buildRegions(create(ResourceSchema), history, undefined);
		expect(region?.current).toBeUndefined();
		expect(region?.history.map((d) => d.id)).toEqual(["release-3", "release-2", "release-1"]);
		expect(region?.lastGood).toBeUndefined();
	});

	test("takes the rollback target the region status names, outside the history page", () => {
		const [current, target] = ["current", "target"].map((id) =>
			create(DeploymentSchema, { id, placements: [{ region: "us-va-1" }], spec: create(DeploymentSpecSchema) }),
		);
		if (current === undefined || target === undefined) throw new Error("Missing deployment");
		const environment = create(EnvironmentStatusSchema, {
			regions: [
				create(RegionStatusSchema, { region: "us-va-1", deploymentId: "current", lastGoodDeploymentId: "target" }),
			],
		});
		const [region] = buildRegions(create(ResourceSchema), [current], environment, [current, target]);
		expect(region?.lastGood?.id).toBe("target");
		expect(region?.history.map((d) => d.id)).toEqual(["current"]);
	});

	test("names the deployment that serves after a failed rollout apart from the live one", () => {
		const [previous, failed] = ["previous", "failed"].map((id) =>
			create(DeploymentSchema, { id, placements: [{ region: "us-va-1" }], spec: create(DeploymentSpecSchema) }),
		);
		if (previous === undefined || failed === undefined) throw new Error("Missing deployment");
		const environment = create(EnvironmentStatusSchema, {
			regions: [
				create(RegionStatusSchema, { region: "us-va-1", deploymentId: "failed", servingDeploymentId: "previous" }),
			],
		});
		const [region] = buildRegions(create(ResourceSchema), [failed, previous], environment);
		expect(region?.current?.id).toBe("failed");
		expect(region?.serving?.id).toBe("previous");
	});

	test("keeps a long-lived current deployment outside the history page", () => {
		const current = create(DeploymentSchema, {
			id: "current-california",
			placements: [{ region: "us-ca-1", live: true }],
			spec: { spec: { case: "service", value: { port: 9090 } } },
		});
		const history = Array.from({ length: 200 }, (_, i) =>
			create(DeploymentSchema, {
				id: `release-${i.toString()}`,
				placements: [{ region: "us-va-1" }],
			}),
		);
		const environment = create(EnvironmentStatusSchema, {
			regions: [create(RegionStatusSchema, { region: "us-ca-1", deploymentId: current.id })],
		});
		const regions = buildRegions(create(ResourceSchema), history, environment, [current]);
		const california = regions.find((region) => region.name === "us-ca-1");
		expect(california?.current).toEqual(current);
		expect(california?.history).toEqual([]);
		const resource = create(ResourceSchema, { spec: { spec: { case: "service", value: {} } } });
		expect(
			isWorker(
				resource,
				regions.flatMap((r) => (r.current === undefined ? [] : [r.current])),
			),
		).toBe(false);
		expect(regions.flatMap((region) => region.history)).toHaveLength(200);
	});

	test("does not substitute an old release for the current deployment", () => {
		const old = create(DeploymentSchema, { id: "old", placements: [{ region: "us-ca-1", live: true }] });
		const environment = create(EnvironmentStatusSchema, {
			regions: [create(RegionStatusSchema, { region: "us-ca-1", deploymentId: "current" })],
		});
		const regions = buildRegions(create(ResourceSchema), [old], environment);
		expect(regions[0]?.current).toBeUndefined();
	});
});

describe("regionDeployment", () => {
	test("finds the region's current, serving and rollback target deployments beside its history page", () => {
		const [current, serving, target, paged] = ["current", "serving", "target", "paged"].map((id) =>
			create(DeploymentSchema, { id }),
		);
		const region: RegionView = {
			name: "us-va-1",
			status: undefined,
			history: paged === undefined ? [] : [paged],
			current,
			serving,
			lastGood: target,
		};
		expect(["current", "serving", "target", "paged"].map((id) => regionDeployment(region, id)?.id)).toEqual([
			"current",
			"serving",
			"target",
			"paged",
		]);
		expect(regionDeployment(region, "")).toBeUndefined();
		expect(regionDeployment(region, "elsewhere")).toBeUndefined();
	});
});

describe("previousDeploymentId", () => {
	test("reads the deployment before this one in each region from its placement", () => {
		const dep = create(DeploymentSchema, {
			placements: [
				{ region: "us-ca-1", previousDeploymentId: "older-ca" },
				{ region: "us-va-1", previousDeploymentId: "" },
			],
		});
		expect(previousDeploymentId(dep, "us-ca-1")).toBe("older-ca");
		expect(previousDeploymentId(dep, "us-va-1")).toBe("");
		expect(previousDeploymentId(dep, "eu-west-1")).toBe("");
	});
});

describe("metricsPending", () => {
	const NOW_MS = 1_700_000_000_000;
	const deployment = (region: string, startedMs: number | undefined) =>
		create(DeploymentSchema, {
			placements: [
				{
					region,
					live: true,
					...(startedMs === undefined ? {} : { startedAt: msToTimestamp(startedMs) }),
				},
			],
		});
	const region = (name: string, startedMs: number | undefined, ...earlier: (number | undefined)[]): RegionView => {
		const current = deployment(name, startedMs);
		return {
			name,
			status: undefined,
			history: [current, ...earlier.map((ms) => deployment(name, ms))],
			current,
			serving: undefined,
			lastGood: undefined,
		};
	};

	test("holds until two minutes after the first start", () => {
		expect(metricsPending([region("us-ca-1", undefined)], NOW_MS)).toBe(true);
		expect(metricsPending([region("us-ca-1", NOW_MS - 119_000)], NOW_MS)).toBe(true);
		expect(metricsPending([region("us-ca-1", NOW_MS - 120_000)], NOW_MS)).toBe(false);
	});

	test("counts from the region that started first", () => {
		expect(metricsPending([region("us-ca-1", NOW_MS - 10_000), region("us-va-1", NOW_MS - 300_000)], NOW_MS)).toBe(
			false,
		);
	});

	test("holds for a redeploy of a first deployment that failed before it started", () => {
		expect(metricsPending([region("us-ca-1", NOW_MS - 30_000, undefined)], NOW_MS)).toBe(true);
	});

	test("ends once an earlier deployment started", () => {
		expect(metricsPending([region("us-ca-1", NOW_MS, NOW_MS - 3_600_000)], NOW_MS)).toBe(false);
		expect(metricsPending([], NOW_MS)).toBe(false);
	});
});

describe("focusRegion", () => {
	test("picks the healthy region that has a release to roll back to", () => {
		const spec = create(DeploymentSpecSchema);
		const california = create(DeploymentSchema, {
			id: "california",
			placements: [{ region: "us-ca-1", live: true }],
			status: DeploymentStatus.RUNNING,
			spec,
		});
		const current = create(DeploymentSchema, {
			id: "current",
			placements: [{ region: "us-va-1", live: true }],
			status: DeploymentStatus.RUNNING,
			spec,
		});
		const previous = create(DeploymentSchema, { id: "previous", status: DeploymentStatus.SUPERSEDED, spec });
		const regions: RegionView[] = [
			{
				name: "us-ca-1",
				status: undefined,
				history: [california],
				current: california,
				serving: california,
				lastGood: undefined,
			},
			{
				name: "us-va-1",
				status: undefined,
				history: [current, previous],
				current,
				serving: current,
				lastGood: previous,
			},
		];
		expect(focusRegion(regions)?.name).toBe("us-va-1");
	});
});

describe("tookMs", () => {
	const at = (ms: number) => msToTimestamp(ms);

	test("a running deployment took until it turned ready", () => {
		const dep = create(DeploymentSchema, { createdAt: at(1_000), startedAt: at(17_000) });
		expect(tookMs(dep)).toBe(16_000);
	});

	test("a superseded deployment keeps the time it took to turn ready", () => {
		const dep = create(DeploymentSchema, { createdAt: at(1_000), startedAt: at(17_000), completedAt: at(241_000) });
		expect(tookMs(dep)).toBe(16_000);
	});

	test("a deployment that never turned ready took until it ended", () => {
		const dep = create(DeploymentSchema, { createdAt: at(1_000), completedAt: at(91_000) });
		expect(tookMs(dep)).toBe(90_000);
	});

	test("is unknown while the deployment is still starting", () => {
		expect(tookMs(create(DeploymentSchema, { createdAt: at(1_000) }))).toBeUndefined();
	});
});

describe("statusEnvironmentId", () => {
	const resource = create(ResourceSchema, {
		environmentStatuses: [
			{ environmentId: "e-prod", environmentSlug: "production" },
			{ environmentId: "e-stg", environmentSlug: "staging" },
		],
	});

	test("finds the requested environment among the ones the resource runs in", () => {
		expect(statusEnvironmentId(resource, "staging")).toBe("e-stg");
		expect(statusEnvironmentId(resource, "e-prod")).toBe("e-prod");
		expect(statusEnvironmentId(resource, "preview")).toBeUndefined();
	});

	test("takes the first environment when none is requested", () => {
		expect(statusEnvironmentId(resource, null)).toBe("e-prod");
		expect(statusEnvironmentId(create(ResourceSchema, {}), null)).toBeUndefined();
	});
});

describe("changedSecrets", () => {
	const START_MS = 1_700_000_000_000;
	const region = (...replicas: { startedMs: number; state?: string }[]): RegionView => ({
		name: "us-ca-1",
		status: create(RegionStatusSchema, {
			region: "us-ca-1",
			replicas: replicas.map((r) =>
				create(ReplicaSchema, { state: r.state ?? "Running", startedAt: msToTimestamp(r.startedMs) }),
			),
		}),
		history: [],
		current: undefined,
		serving: undefined,
		lastGood: undefined,
	});
	const secret = (name: string, updatedMs: number, users: string[]) =>
		create(SecretSchema, {
			name,
			updatedAt: msToTimestamp(updatedMs),
			usedBy: users.map((resourceId) => create(SecretUserSchema, { resourceId })),
		});

	test("names the secrets this resource reads that changed after its oldest replica started", () => {
		const secrets = [
			secret("STRIPE_SECRET_KEY", START_MS + 60_000, [RESOURCE_ID]),
			secret("DATABASE_URL", START_MS - 60_000, [RESOURCE_ID]),
			secret("OTHER_KEY", START_MS + 60_000, [PRODUCTION_ID]),
		];
		expect(
			changedSecrets(secrets, RESOURCE_ID, [region({ startedMs: START_MS }, { startedMs: START_MS + 120_000 })]),
		).toEqual(["STRIPE_SECRET_KEY"]);
	});

	test("names nothing once every running replica started after the change", () => {
		const secrets = [secret("STRIPE_SECRET_KEY", START_MS, [RESOURCE_ID])];
		expect(
			changedSecrets(secrets, RESOURCE_ID, [
				region({ startedMs: START_MS + 1_000 }, { startedMs: START_MS - 1_000, state: "Stopping" }),
			]),
		).toEqual([]);
	});

	test("names nothing without a running replica", () => {
		expect(changedSecrets([secret("STRIPE_SECRET_KEY", START_MS, [RESOURCE_ID])], RESOURCE_ID, [region()])).toEqual([]);
	});
});
