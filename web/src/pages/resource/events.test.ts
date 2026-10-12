import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { ClusterAccessSchema, type ClusterAccess } from "@gen/loco/observability/v1/observability_access_pb";
import { KubernetesEventSchema, type KubernetesEvent } from "@gen/loco/observability/v1/observability_pb";

import { msToTimestamp } from "@/lib/time";

import {
	EVENT_LIMIT,
	EVENT_WINDOW_MS,
	eventProxyUrls,
	eventsKey,
	eventsRequest,
	filterEvents,
	mergeEvents,
	objectLabel,
	type EventsTarget,
} from "./events";

const WORKSPACE_ID = "0199d1c2-0000-7000-8000-000000000010";
const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const PRODUCTION_ID = "0199d1c2-0000-7000-8000-000000000002";
const STAGING_ID = "0199d1c2-0000-7000-8000-000000000003";
const EAST_CLUSTER_ID = "0199d1c2-0000-7000-8000-000000000004";
const WEST_CLUSTER_ID = "0199d1c2-0000-7000-8000-000000000005";
const IDLE_CLUSTER_ID = "0199d1c2-0000-7000-8000-000000000006";
const EAST_PROXY = "https://obs.us-east-1.example";
const WEST_PROXY = "https://obs.eu-west-1.example";
const IDLE_PROXY = "https://obs.ap-south-1.example";
const NOW_MS = Date.UTC(2026, 9, 11, 12, 0, 0);
const SECOND_MS = 1_000;
const POD = "resource-web-7d9f8b6c5d-x2k4p";

function deployment(clusterId: string): Deployment {
	return create(DeploymentSchema, { resourceId: RESOURCE_ID, environmentId: PRODUCTION_ID, clusterId });
}

function cluster(clusterId: string, proxyUrl: string): ClusterAccess {
	return create(ClusterAccessSchema, { clusterId, proxyUrl });
}

function event(reason: string, type: string, ageSeconds: number, message = ""): KubernetesEvent {
	return create(KubernetesEventSchema, {
		timestamp: msToTimestamp(NOW_MS - ageSeconds * SECOND_MS),
		type,
		reason,
		message,
		objectKind: "Pod",
		objectName: POD,
		count: 1n,
		resourceId: RESOURCE_ID,
	});
}

const target: EventsTarget = { workspaceId: WORKSPACE_ID, environmentId: PRODUCTION_ID, resourceId: RESOURCE_ID };

describe("eventProxyUrls", () => {
	const clusters = [cluster(EAST_CLUSTER_ID, EAST_PROXY), cluster(WEST_CLUSTER_ID, WEST_PROXY), cluster(IDLE_CLUSTER_ID, IDLE_PROXY)];

	test("asks the proxy of each cluster that runs one of the environment's deployments, once", () => {
		const deployments = [deployment(WEST_CLUSTER_ID), deployment(EAST_CLUSTER_ID), deployment(EAST_CLUSTER_ID)];
		expect(eventProxyUrls(clusters, deployments)).toEqual([WEST_PROXY, EAST_PROXY].toSorted());
	});

	test("asks no proxy when the environment has no deployment or its cluster has no proxy", () => {
		expect(eventProxyUrls(clusters, [])).toEqual([]);
		expect(eventProxyUrls([cluster(EAST_CLUSTER_ID, "")], [deployment(EAST_CLUSTER_ID)])).toEqual([]);
	});
});

describe("eventsRequest", () => {
	test("names the workspace, the active environment and the resource over the last day", () => {
		const request = eventsRequest(target, NOW_MS);
		expect(request.workspaceId).toBe(WORKSPACE_ID);
		expect(request.environmentId).toBe(PRODUCTION_ID);
		expect(request.resourceIds).toEqual([RESOURCE_ID]);
		expect(request.startTime).toEqual(msToTimestamp(NOW_MS - EVENT_WINDOW_MS));
		expect(request.endTime).toEqual(msToTimestamp(NOW_MS));
		expect(request.limit).toBe(EVENT_LIMIT);
	});

	test("keys the query by environment so switching environments refetches", () => {
		const staging = { ...target, environmentId: STAGING_ID };
		expect(eventsKey(target, [EAST_PROXY])).not.toEqual(eventsKey(staging, [EAST_PROXY]));
		expect(eventsKey(target, [EAST_PROXY])).not.toEqual(eventsKey(target, [WEST_PROXY]));
	});
});

describe("mergeEvents", () => {
	test("merges every region's events newest first", () => {
		const east = [event("Scheduled", "Normal", 30), event("Pulled", "Normal", 10)];
		const west = [event("BackOff", "Warning", 20)];
		expect(mergeEvents([east, west]).map((e) => e.reason)).toEqual(["Pulled", "BackOff", "Scheduled"]);
	});

	test("keeps at most the request limit", () => {
		const many = Array.from({ length: EVENT_LIMIT + 1 }, (_, i) => event(`Reason${String(i)}`, "Normal", i));
		const merged = mergeEvents([many]);
		expect(merged).toHaveLength(EVENT_LIMIT);
		expect(merged[0]?.reason).toBe("Reason0");
	});
});

describe("filterEvents", () => {
	const events = [
		event("BackOff", "Warning", 1, "Back-off restarting failed container"),
		event("Scheduled", "Normal", 2, "Successfully assigned"),
		event("Unhealthy", "Warning", 3, "Readiness probe failed"),
	];

	test("counts warnings and normal events and keeps the chosen type", () => {
		const { rows, counts } = filterEvents(events, "", "warning");
		expect(counts).toEqual({ all: 3, warning: 2, normal: 1 });
		expect(rows.map((e) => e.reason)).toEqual(["BackOff", "Unhealthy"]);
	});

	test("searches reason, message and object, and counts only matches", () => {
		expect(filterEvents(events, "probe", "all").rows.map((e) => e.reason)).toEqual(["Unhealthy"]);
		expect(filterEvents(events, "x2k4p", "all").counts.all).toBe(3);
		expect(filterEvents(events, "nothing", "all").counts).toEqual({ all: 0, warning: 0, normal: 0 });
	});
});

describe("objectLabel", () => {
	test("joins kind and name", () => {
		expect(objectLabel(event("Started", "Normal", 0))).toBe(`Pod/${POD}`);
	});
});
