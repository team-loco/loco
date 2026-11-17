import type { Timestamp } from "@bufbuild/protobuf/wkt";
import type { Deployment, Replica } from "@gen/loco/deployment/v1/deployment_pb";

import type { BadgeTone } from "@/components/design/Badge";
import { getServiceSpec, isInFlight } from "@/lib/deployment-utils";
import { maybeTsMs, tsMs } from "@/lib/time";

import { lastGoodDeployment, type RegionView } from "./model";

export function progressDeployments(regions: readonly RegionView[]) {
	return regions.flatMap((region) => {
		const history = [
			...new Map(
				[...region.history, ...(region.current === undefined ? [] : [region.current])].map((d) => [d.id, d]),
			).values(),
		].toSorted((a, b) => tsMs(b.createdAt) - tsMs(a.createdAt));
		const deployment = history[0];
		if (deployment === undefined) return [];
		return [{ region, deployment, previous: lastGoodDeployment({ ...region, current: deployment, history }) }];
	});
}

export function deploymentElapsedMs(deployment: Deployment, nowMs: number): number | undefined {
	const created = maybeTsMs(deployment.createdAt);
	if (created === undefined) return undefined;
	const end = isInFlight(deployment.status)
		? nowMs
		: (maybeTsMs(deployment.completedAt) ?? maybeTsMs(deployment.startedAt) ?? nowMs);
	return Math.max(0, end - created);
}

export function groupReplicas(replicas: readonly Replica[], revision: bigint) {
	return [
		{ label: "New version", replicas: replicas.filter((r) => r.placementRevision === revision) },
		{ label: "Previous version", replicas: replicas.filter((r) => r.placementRevision !== revision) },
	];
}

export function servingDeployments(replicas: readonly Replica[]) {
	const counts = new Map<string, number>();
	for (const replica of replicas) {
		if (replica.ready && replica.deploymentId !== "")
			counts.set(replica.deploymentId, (counts.get(replica.deploymentId) ?? 0) + 1);
	}
	return [...counts].map(([id, readyReplicas]) => ({ id, readyReplicas }));
}

export function replicaState(state: string): { label: string; tone: BadgeTone } {
	switch (state) {
		case "Scheduling":
			return { label: "Waiting for a node", tone: "muted" };
		case "Pulling":
			return { label: "Pulling image", tone: "info" };
		case "Starting":
			return { label: state, tone: "info" };
		case "Serving":
			return { label: state, tone: "ok" };
		case "Failing":
		case "Killed":
			return { label: state, tone: "bad" };
		case "Stopping":
			return { label: state, tone: "warn" };
		case "Stopped":
			return { label: state, tone: "muted" };
		default:
			return { label: "Unknown", tone: "muted" };
	}
}

export function stoppingCountdown(replica: Replica, nowMs: number) {
	const deadline = maybeTsMs(replica.stateDeadline);
	if (replica.state !== "Stopping" || deadline === undefined) return undefined;
	const since = maybeTsMs(replica.stateSince);
	const duration = since === undefined ? undefined : Math.max(0, deadline - since);
	const remaining = Math.max(0, Math.min(deadline - nowMs, duration ?? Infinity));
	return {
		seconds: Math.ceil(remaining / 1000),
		percent: duration === undefined ? undefined : duration === 0 ? 0 : (remaining / duration) * 100,
	};
}

export function activityTime(at: Timestamp | undefined, startedAt: Timestamp | undefined): string {
	const atMs = maybeTsMs(at);
	const startedMs = maybeTsMs(startedAt);
	if (atMs === undefined || startedMs === undefined) return "—";
	const seconds = Math.max(0, Math.floor((atMs - startedMs) / 1000));
	return `+${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

export function problemLogsHref(logsHref: string | undefined, deployment: Deployment): string | undefined {
	if (logsHref === undefined || deployment.problem === undefined) return undefined;
	const buildId = getServiceSpec(deployment)?.build?.buildId;
	const query =
		deployment.problem.reason === "build_failed" && buildId
			? `@build:${buildId}`
			: deployment.problem.replicaName !== ""
				? `@replica:${deployment.problem.replicaName}`
				: undefined;
	if (query === undefined) return undefined;
	const [path, search] = logsHref.split("?");
	const params = new URLSearchParams(search);
	params.set("q", query);
	return `${path}?${params}`;
}
