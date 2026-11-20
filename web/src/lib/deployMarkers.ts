import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentImage } from "@/lib/deployment-utils";
import { imageVersion } from "@/lib/image";
import { agoLabel, tsMs } from "@/lib/time";

const EDGE = 0.015;
const MAX_NAMES = 3;
const BAD_SHARE = 0.3;

export interface DeployMarker {
	/** Position along the sparkline, 0 to 1. */
	at: number;
	failed: boolean;
	count: number;
	tip: string;
}

interface Placed {
	fraction: number;
	failed: boolean;
	version: string;
	resource: string;
	createdMs: number;
}

/** Groups the deployments created in [startMs, endMs) into at most `buckets` markers. */
export function deployMarkers(
	deployments: { deployment: Deployment; resource: string }[],
	startMs: number,
	endMs: number,
	buckets: number,
	nowMs: number,
): DeployMarker[] {
	const span = endMs - startMs;
	const groups = new Map<number, Placed[]>();
	for (const { deployment, resource } of deployments) {
		const createdMs = tsMs(deployment.createdAt);
		if (createdMs < startMs || createdMs >= endMs) continue;
		const fraction = (createdMs - startMs) / span;
		const bucket = Math.min(buckets - 1, Math.floor(fraction * buckets));
		const placed: Placed = {
			fraction,
			failed: deployment.status === DeploymentStatus.FAILED,
			version: imageVersion(deploymentImage(deployment)),
			resource,
			createdMs,
		};
		groups.set(bucket, [...(groups.get(bucket) ?? []), placed]);
	}
	return [...groups.entries()]
		.toSorted(([a], [b]) => a - b)
		.map(([, group]) => {
			const failures = group.filter((d) => d.failed).length;
			const mean = group.reduce((sum, d) => sum + d.fraction, 0) / group.length;
			return {
				at: Math.min(1 - EDGE, Math.max(EDGE, mean)),
				failed: group.length === 1 ? failures === 1 : failures / group.length >= BAD_SHARE,
				count: group.length,
				tip: markerTip(group, failures, nowMs),
			};
		});
}

function markerTip(group: Placed[], failures: number, nowMs: number): string {
	const [only] = group;
	if (group.length === 1 && only !== undefined) {
		const verb = only.failed ? "failed" : "deployed";
		return `${only.version} · ${only.resource}\n${verb} ${agoLabel(only.createdMs, nowMs)}`;
	}
	const names = [...new Set(group.map((d) => d.resource))];
	const more = names.length > MAX_NAMES ? ` +${(names.length - MAX_NAMES).toString()}` : "";
	const failed = failures > 0 ? ` · ${failures.toString()} failed` : "";
	return `${group.length.toString()} deploys${failed}\n${names.slice(0, MAX_NAMES).join(", ")}${more}`;
}
