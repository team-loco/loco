import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { DeploymentStatus, type Activity, type Deployment, type Replica } from "@gen/loco/deployment/v1/deployment_pb";

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

export function deploymentStatusLabel(deployment: Deployment): string {
	const labels: Record<DeploymentStatus, string> = {
		[DeploymentStatus.UNSPECIFIED]: "Unknown",
		[DeploymentStatus.BUILDING]: "Building",
		[DeploymentStatus.DEPLOYING]: "Deploying",
		[DeploymentStatus.RUNNING]: "Running",
		[DeploymentStatus.FAILED]: "Failed",
		[DeploymentStatus.CANCELED]: "Canceled",
		[DeploymentStatus.SUPERSEDED]: "Superseded",
	};
	return (
		labels[deployment.status] +
		(deployment.status === DeploymentStatus.RUNNING && deployment.problem !== undefined ? " · problem" : "")
	);
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

export function activityRows(activity: readonly Activity[], startedAt: Timestamp | undefined) {
	const started = maybeTsMs(startedAt);
	return activity
		.toSorted((a, b) => (a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : 0))
		.map((entry) => {
			const at = maybeTsMs(entry.at);
			const seconds =
				started === undefined || at === undefined ? undefined : Math.max(0, Math.floor((at - started) / 1000));
			return {
				key: String(entry.seq),
				time: seconds === undefined ? "—" : `+${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`,
				severity: entry.severity,
				replicaName: entry.replicaName,
				message: entry.message,
			};
		});
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
