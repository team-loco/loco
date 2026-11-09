import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentPhase, DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { msToTimestamp } from "@/lib/time";

import {
	ALL_DEPLOYMENTS,
	DEPLOYMENT_LIMIT,
	DEPLOYMENT_WINDOW_MS,
	deploymentCounts,
	deploymentState,
	filterDeployments,
	withinWindow,
} from "./deploymentRows";

const NOW = 1_800_000_000_000;

function row(id: string, status: DeploymentPhase, isActive: boolean, ageMs = 0) {
	return { deployment: create(DeploymentSchema, { id, status, isActive, createdAt: msToTimestamp(NOW - ageMs) }) };
}

describe("deploymentState", () => {
	test("a finished deployment that is no longer active is superseded", () => {
		expect(deploymentState(row("a", DeploymentPhase.RUNNING, false).deployment)).toBe("superseded");
		expect(deploymentState(row("b", DeploymentPhase.SUCCEEDED, false).deployment)).toBe("superseded");
		expect(deploymentState(row("c", DeploymentPhase.RUNNING, true).deployment)).toBe("running");
		expect(deploymentState(row("d", DeploymentPhase.FAILED, false).deployment)).toBe("failed");
		expect(deploymentState(row("e", DeploymentPhase.PENDING, true).deployment)).toBe("progress");
	});
});

describe("filters", () => {
	const rows = [
		row("live", DeploymentPhase.RUNNING, true),
		row("old", DeploymentPhase.RUNNING, false),
		row("rolling", DeploymentPhase.DEPLOYING, true),
		row("broken", DeploymentPhase.FAILED, true),
	];

	test("counts each state and filters to one", () => {
		const counts = deploymentCounts(rows);
		expect(counts.get("running")).toBe(1);
		expect(counts.get("superseded")).toBe(1);
		expect(counts.get("progress")).toBe(1);
		expect(counts.get("failed")).toBe(1);
		expect(filterDeployments(rows, "failed").map((r) => r.deployment.id)).toEqual(["broken"]);
		expect(filterDeployments(rows, ALL_DEPLOYMENTS)).toHaveLength(rows.length);
	});

	test("keeps the last 24 hours and shows at most ten rows", () => {
		const many = Array.from({ length: DEPLOYMENT_LIMIT + 2 }, (_, i) =>
			row(`d${i.toString()}`, DeploymentPhase.FAILED, true),
		);
		const stale = row("stale", DeploymentPhase.FAILED, true, DEPLOYMENT_WINDOW_MS + 1);
		const recent = withinWindow([...many, stale], NOW);
		expect(recent).toHaveLength(DEPLOYMENT_LIMIT + 2);
		expect(filterDeployments(recent, "failed")).toHaveLength(DEPLOYMENT_LIMIT);
	});
});
