import { DeploymentStatus, type DeploymentBucket } from "@gen/loco/deployment/v1/deployment_pb";

import { imageVersion } from "@/lib/image";
import { agoLabel, tsMs } from "@/lib/time";

const EDGE = 0.015;
const BAD_SHARE = 0.3;

/** The most resource names a marker's tip lists. */
export const MARKER_NAMES = 3;

export interface DeployMarker {
	/** Position along the sparkline, 0 to 1. */
	at: number;
	failed: boolean;
	count: number;
	tip: string;
}

function failures(bucket: DeploymentBucket): number {
	return bucket.statusCounts.find((c) => c.status === DeploymentStatus.FAILED)?.count ?? 0;
}

/** One marker per bucket of deployments created in [startMs, endMs). */
export function deployMarkers(
	buckets: readonly DeploymentBucket[],
	startMs: number,
	endMs: number,
	nowMs: number,
): DeployMarker[] {
	return buckets.map((bucket) => {
		const failed = failures(bucket);
		const mean = (tsMs(bucket.meanCreatedAt) - startMs) / (endMs - startMs);
		return {
			at: Math.min(1 - EDGE, Math.max(EDGE, mean)),
			failed: bucket.deploymentCount === 1 ? failed === 1 : failed / bucket.deploymentCount >= BAD_SHARE,
			count: bucket.deploymentCount,
			tip: markerTip(bucket, failed, nowMs),
		};
	});
}

function markerTip(bucket: DeploymentBucket, failed: number, nowMs: number): string {
	const only = bucket.onlyDeployment;
	if (bucket.deploymentCount === 1 && only !== undefined) {
		const verb = only.status === DeploymentStatus.FAILED ? "failed" : "deployed";
		return `${imageVersion(only.image)} · ${only.resourceName}\n${verb} ${agoLabel(tsMs(only.createdAt), nowMs)}`;
	}
	const extra = bucket.resourceCount - bucket.resourceNames.length;
	const more = extra > 0 ? ` +${extra.toString()}` : "";
	const failedNote = failed > 0 ? ` · ${failed.toString()} failed` : "";
	return `${bucket.deploymentCount.toString()} deploys${failedNote}\n${bucket.resourceNames.join(", ")}${more}`;
}
