import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentBucketSchema, DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";

import { HOUR_MS } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";

import { deployMarkers } from "./deployMarkers";

const START = 1_760_000_000_000;
const END = START + 24 * HOUR_MS;

function single(hoursIn: number, status = DeploymentStatus.RUNNING) {
	const createdAt = msToTimestamp(START + hoursIn * HOUR_MS);
	return create(DeploymentBucketSchema, {
		deploymentCount: 1,
		meanCreatedAt: createdAt,
		statusCounts: [{ status, count: 1 }],
		resourceCount: 1,
		resourceNames: ["web"],
		onlyDeployment: { resourceName: "web", status, createdAt, image: "ghcr.io/acme/web:v2" },
	});
}

describe("deployMarkers", () => {
	test("places a marker at its bucket's mean creation time, inside the edges", () => {
		const markers = deployMarkers([single(1), single(0)], START, END, END);
		expect(markers[0]?.at).toBeCloseTo(1 / 24);
		expect(markers[1]?.at).toBeCloseTo(0.015);
	});

	test("summarizes a bucket of several deploys and flags a high failure share", () => {
		const [marker] = deployMarkers(
			[
				create(DeploymentBucketSchema, {
					deploymentCount: 3,
					meanCreatedAt: msToTimestamp(START + HOUR_MS),
					statusCounts: [
						{ status: DeploymentStatus.RUNNING, count: 2 },
						{ status: DeploymentStatus.FAILED, count: 1 },
					],
					resourceCount: 5,
					resourceNames: ["web", "api", "worker"],
				}),
			],
			START,
			END,
			END,
		);
		expect(marker?.count).toBe(3);
		expect(marker?.failed).toBe(true);
		expect(marker?.tip).toBe("3 deploys · 1 failed\nweb, api, worker +2");
	});

	test("names a single failed deployment's version and resource", () => {
		const [marker] = deployMarkers([single(2, DeploymentStatus.FAILED)], START, END, START + 3 * HOUR_MS);
		expect(marker?.failed).toBe(true);
		expect(marker?.tip).toBe("v2 · web\nfailed 1h ago");
	});
});
