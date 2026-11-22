import { DeploymentStatus, type DeploymentBucket } from "@gen/loco/deployment/v1/deployment_pb";

import { imageTag } from "@/lib/image";
import { agoLabel, maybeTsMs, tsMs } from "@/lib/time";

const EDGE = 0.015;
const BAD_SHARE = 0.3;

/** The most resource names a marker's tip lists. */
export const MARKER_NAMES = 3;

/** A bucket of deployments as charts show it. */
export interface DeploySummary {
	/** The deployments' mean creation time. */
	ms: number;
	count: number;
	failed: number;
	/** The resource names the bucket lists, and how many more it deployed. */
	names: string[];
	moreNames: number;
	/** The bucket's deployment when it holds exactly one. */
	only: { version: string; resource: string; createdMs: number; failed: boolean } | undefined;
}

/** The summary of each bucket with a mean creation time. */
export function deploySummaries(buckets: readonly DeploymentBucket[]): DeploySummary[] {
	return buckets.flatMap((bucket) => {
		const ms = maybeTsMs(bucket.meanCreatedAt);
		if (ms === undefined) return [];
		const only = bucket.deploymentCount === 1 ? bucket.onlyDeployment : undefined;
		return [
			{
				ms,
				count: bucket.deploymentCount,
				failed: bucket.statusCounts.find((c) => c.status === DeploymentStatus.FAILED)?.count ?? 0,
				names: bucket.resourceNames,
				moreNames: Math.max(0, bucket.resourceCount - bucket.resourceNames.length),
				only:
					only === undefined
						? undefined
						: {
								version: imageTag(only.image),
								resource: only.resourceName,
								createdMs: tsMs(only.createdAt),
								failed: only.status === DeploymentStatus.FAILED,
							},
			},
		];
	});
}

export interface DeployMarker {
	/** Position along the sparkline, 0 to 1. */
	at: number;
	failed: boolean;
	count: number;
	tip: string;
}

/** One sparkline marker per bucket of deployments created in [startMs, endMs). */
export function deployMarkers(
	buckets: readonly DeploymentBucket[],
	startMs: number,
	endMs: number,
	nowMs: number,
): DeployMarker[] {
	return deploySummaries(buckets).map((s) => ({
		at: Math.min(1 - EDGE, Math.max(EDGE, (s.ms - startMs) / (endMs - startMs))),
		failed: s.only === undefined ? s.failed / s.count >= BAD_SHARE : s.only.failed,
		count: s.count,
		tip: markerTip(s, nowMs),
	}));
}

function markerTip(s: DeploySummary, nowMs: number): string {
	if (s.only !== undefined) {
		const verb = s.only.failed ? "failed" : "deployed";
		return `${s.only.version}, ${s.only.resource}\n${verb} ${agoLabel(s.only.createdMs, nowMs)}`;
	}
	const more = s.moreNames > 0 ? ` +${s.moreNames.toString()}` : "";
	const failed = s.failed > 0 ? `, ${s.failed.toString()} failed` : "";
	return `${s.count.toString()} deploys${failed}\n${s.names.join(", ")}${more}`;
}
