import { create } from "@bufbuild/protobuf";
import { DeploymentStatus, DeploymentSchema, DeploymentSpecSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { skipToken } from "@connectrpc/connect-query";
import { describe, expect, test } from "bun:test";
import { EnvironmentStatusSchema, RegionStatusSchema, ResourceSchema } from "@gen/loco/resource/v1/resource_pb";

import { isWorker } from "@/lib/resource-regions";

import { msToTimestamp } from "@/lib/time";

import {
	buildRegions,
	deploymentsRequest,
	focusRegion,
	lastGoodDeployment,
	statusEnvironmentId,
	tookMs,
	type RegionView,
} from "./model";

const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const PRODUCTION_ID = "0199d1c2-0000-7000-8000-000000000002";

describe("deploymentsRequest", () => {
	test("asks only for the deployments of the active environment", () => {
		expect(deploymentsRequest(RESOURCE_ID, PRODUCTION_ID)).toEqual({
			resourceId: RESOURCE_ID,
			environmentId: PRODUCTION_ID,
			pageSize: 200,
			pageToken: "",
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
		if (region === undefined) throw new Error("Missing history region");
		expect(lastGoodDeployment(region)?.id).toBe("release-3");
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

describe("lastGoodDeployment", () => {
	const spec = create(DeploymentSpecSchema);
	const failed = create(DeploymentSchema, { id: "failed", status: DeploymentStatus.FAILED, spec });
	const superseded = create(DeploymentSchema, { id: "superseded", status: DeploymentStatus.SUPERSEDED, spec });
	const older = create(DeploymentSchema, { id: "older", status: DeploymentStatus.SUPERSEDED, spec });

	test("skips the current and failed deployments", () => {
		const region: RegionView = {
			name: "us-va-1",
			status: undefined,
			history: [failed, superseded, older],
			current: failed,
		};
		expect(lastGoodDeployment(region)?.id).toBe("superseded");
	});

	test("finds nothing when the region has one deployment", () => {
		const region: RegionView = { name: "us-va-1", status: undefined, history: [failed], current: failed };
		expect(lastGoodDeployment(region)).toBeUndefined();
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
			{ name: "us-ca-1", status: undefined, history: [california], current: california },
			{ name: "us-va-1", status: undefined, history: [current, previous], current },
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
