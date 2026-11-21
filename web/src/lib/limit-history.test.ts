import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import {
	DeploymentPlacementSchema,
	DeploymentSchema,
	DeploymentSpecSchema,
	PlacementStatus,
	RegionDeploymentTargetSchema,
	ServiceDeploymentSpecSchema,
} from "@gen/loco/deployment/v1/deployment_pb";

import { limitAt, limitSteps, type Sizing } from "@/lib/limit-history";
import { parseCpuMilli } from "@/lib/quantity";
import { msToTimestamp } from "@/lib/time";

const REGION = "us-east";
const HOUR_MS = 3_600_000;
const cpu = (target: Sizing) => parseCpuMilli(target.cpu ?? "");

function deployment(id: string, startedMs: number, size: string, status = PlacementStatus.SUPERSEDED) {
	return create(DeploymentSchema, {
		id,
		spec: create(DeploymentSpecSchema, {
			spec: {
				case: "service",
				value: create(ServiceDeploymentSpecSchema, {
					regions: { [REGION]: create(RegionDeploymentTargetSchema, { cpu: size }) },
				}),
			},
		}),
		placements: [create(DeploymentPlacementSchema, { region: REGION, status, startedAt: msToTimestamp(startedMs) })],
	});
}

describe("limitSteps", () => {
	test("steps the limit at each deployment's start, oldest first", () => {
		const steps = limitSteps(
			[deployment("b", 2 * HOUR_MS, "200m", PlacementStatus.RUNNING), deployment("a", HOUR_MS, "100m")],
			REGION,
			cpu,
			undefined,
		);
		expect(steps).toEqual([
			{ from: HOUR_MS, limit: 100 },
			{ from: 2 * HOUR_MS, limit: 200 },
		]);
	});

	test("skips deployments that never ran in the region", () => {
		const steps = limitSteps(
			[deployment("a", HOUR_MS, "100m"), deployment("b", 2 * HOUR_MS, "200m", PlacementStatus.FAILED)],
			REGION,
			cpu,
			undefined,
		);
		expect(steps).toEqual([{ from: HOUR_MS, limit: 100 }]);
	});

	test("falls back to the current target without any started deployment", () => {
		const current = create(RegionDeploymentTargetSchema, { cpu: "250m" });
		expect(limitSteps([], REGION, cpu, current)).toEqual([{ from: -Infinity, limit: 250 }]);
		expect(limitSteps([], REGION, cpu, undefined)).toEqual([]);
	});
});

describe("limitAt", () => {
	const steps = [
		{ from: HOUR_MS, limit: 100 },
		{ from: 2 * HOUR_MS, limit: 200 },
	];

	test("converts a point taken before a resize with the old limit", () => {
		expect(limitAt(steps, HOUR_MS + 1)).toBe(100);
		expect(limitAt(steps, 2 * HOUR_MS)).toBe(200);
	});

	test("uses the oldest known limit before the first step", () => {
		expect(limitAt(steps, 0)).toBe(100);
		expect(limitAt([], 0)).toBeUndefined();
	});
});
