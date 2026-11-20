import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentSchema, DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";

import { HOUR_MS } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";

import { deployMarkers } from "./deployMarkers";

const START = 1_760_000_000_000;
const END = START + 24 * HOUR_MS;

function at(hoursIn: number, status = DeploymentStatus.RUNNING, resource = "web") {
	return {
		resource: { name: resource },
		deployment: create(DeploymentSchema, { createdAt: msToTimestamp(START + hoursIn * HOUR_MS), status }),
	};
}

describe("deployMarkers", () => {
	test("keeps one marker per deployment outside a shared bucket and ignores the rest of time", () => {
		const markers = deployMarkers([at(1), at(13), at(30), at(-2)], START, END, 12, END);
		expect(markers.map((m) => m.count)).toEqual([1, 1]);
		expect(markers[0]?.at).toBeCloseTo(1 / 24);
	});

	test("groups deployments sharing a bucket and flags failures", () => {
		const markers = deployMarkers([at(0.5), at(1, DeploymentStatus.FAILED, "api")], START, END, 12, END);
		expect(markers).toHaveLength(1);
		expect(markers[0]?.count).toBe(2);
		expect(markers[0]?.failed).toBe(true);
		expect(markers[0]?.tip).toBe("2 deploys · 1 failed\nweb, api");
	});

	test("marks a single failed deployment as failed and names the resource", () => {
		const [marker] = deployMarkers([at(2, DeploymentStatus.FAILED)], START, END, 12, START + 3 * HOUR_MS);
		expect(marker?.failed).toBe(true);
		expect(marker?.tip).toContain("· web\nfailed 1h ago");
	});
});
