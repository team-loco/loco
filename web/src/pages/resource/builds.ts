import { BuildStatus, type Build } from "@gen/loco/build/v1/build_pb";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import type { Glyph } from "@/components/design/StatusGlyph";
import { getServiceSpec } from "@/lib/deployment-utils";
import { shortDigest } from "@/lib/image";
import { QUERY_MAX_MS } from "@/lib/obs-time";
import { maybeTsMs, tsMs } from "@/lib/time";

const SECOND_MS = 1000;
const SECONDS_PER_MINUTE = 60;
const MINUTES_PER_HOUR = 60;
const FINISHED_LOG_MARGIN_MS = 60_000;

interface BuildStatusStyle {
	label: string;
	glyph: Glyph;
}

export function buildStatusStyle(status: BuildStatus): BuildStatusStyle {
	switch (status) {
		case BuildStatus.AWAITING_UPLOAD:
			return { label: "Awaiting upload", glyph: "draft" };
		case BuildStatus.QUEUED:
			return { label: "Queued", glyph: "draft" };
		case BuildStatus.RUNNING:
			return { label: "Building", glyph: "spin" };
		case BuildStatus.SUCCEEDED:
			return { label: "Succeeded", glyph: "ok" };
		case BuildStatus.FAILED:
			return { label: "Failed", glyph: "err" };
		case BuildStatus.CANCELED:
			return { label: "Canceled", glyph: "off" };
		case BuildStatus.UNSPECIFIED:
			return { label: "Pending", glyph: "draft" };
	}
}

export function isBuildFinished(status: BuildStatus): boolean {
	return status === BuildStatus.SUCCEEDED || status === BuildStatus.FAILED || status === BuildStatus.CANCELED;
}

export function canCancelBuild(status: BuildStatus): boolean {
	return status === BuildStatus.AWAITING_UPLOAD || status === BuildStatus.QUEUED || status === BuildStatus.RUNNING;
}

export function buildTookMs(build: Build): number | undefined {
	const started = maybeTsMs(build.startedAt);
	const finished = maybeTsMs(build.finishedAt);
	if (started === undefined || finished === undefined) return undefined;
	return Math.max(0, finished - started);
}

export function formatBuildDuration(ms: number): string {
	const totalSeconds = Math.floor(ms / SECOND_MS);
	const seconds = totalSeconds % SECONDS_PER_MINUTE;
	const totalMinutes = Math.floor(totalSeconds / SECONDS_PER_MINUTE);
	const minutes = totalMinutes % MINUTES_PER_HOUR;
	const hours = Math.floor(totalMinutes / MINUTES_PER_HOUR);
	if (hours > 0) return `${hours.toString()}h ${minutes.toString()}m`;
	if (minutes > 0) return `${minutes.toString()}m ${seconds.toString().padStart(2, "0")}s`;
	return `${seconds.toString()}s`;
}

export function buildImageLabel(build: Build): { text: string; muted: boolean } {
	const digest = build.imageDigest ?? "";
	if (digest !== "") return { text: shortDigest(digest), muted: false };
	return { text: isBuildFinished(build.status) ? "no image" : "pending", muted: true };
}

export function buildMessage(build: Build): string | undefined {
	if (build.status !== BuildStatus.FAILED && build.status !== BuildStatus.CANCELED) return undefined;
	return build.message === "" ? undefined : build.message;
}

export function emptyBuildLogText(status: BuildStatus): string {
	if (status === BuildStatus.AWAITING_UPLOAD) return "Waiting for the CLI to upload source.";
	if (!isBuildFinished(status)) return "Waiting for build output.";
	return "No logs for this build.";
}

export type BuildLogSource =
	{ kind: "none" } | { kind: "tail"; sinceMs: number } | { kind: "query"; startMs: number; endMs: number };

export function buildLogSource(build: Build): BuildLogSource {
	if (build.status === BuildStatus.AWAITING_UPLOAD) return { kind: "none" };
	const createdMs = tsMs(build.createdAt);
	const finishedMs = maybeTsMs(build.finishedAt);
	if (!isBuildFinished(build.status) || finishedMs === undefined) return { kind: "tail", sinceMs: createdMs };
	const endMs = finishedMs + FINISHED_LOG_MARGIN_MS;
	return { kind: "query", startMs: Math.max(createdMs, endMs - QUERY_MAX_MS), endMs };
}

export function deploymentBuildId(dep: Deployment): string | undefined {
	const id = getServiceSpec(dep)?.build?.buildId ?? "";
	return id === "" ? undefined : id;
}

export function deploymentsByBuild<T extends { dep: Deployment }>(rows: T[]): Map<string, T> {
	const byBuild = new Map<string, T>();
	for (const row of rows.toSorted((a, b) => tsMs(b.dep.createdAt) - tsMs(a.dep.createdAt))) {
		const id = deploymentBuildId(row.dep);
		if (id !== undefined && !byBuild.has(id)) byBuild.set(id, row);
	}
	return byBuild;
}

export function pageOf(index: number, size: number): number {
	return Math.max(0, Math.floor(index / size));
}
