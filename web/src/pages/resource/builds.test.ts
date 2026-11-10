import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { BuildSchema, BuildStatus } from "@gen/loco/build/v1/build_pb";
import { DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { QUERY_MAX_MS } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";

import {
	buildImageLabel,
	buildLogSource,
	buildMessage,
	buildTookMs,
	canCancelBuild,
	deploymentsByBuild,
	emptyBuildLogText,
	formatBuildDuration,
	pageOf,
} from "./builds";

const BUILD_ID = "0199d1c2-0000-7000-8000-00000000b001";
const DIGEST = "sha256:7c41e9b2d6f0a18c4e00000000000000000000000000000000000000000000ff";
const CREATED_MS = 1_760_000_000_000;
const MINUTE_MS = 60_000;

function build(status: BuildStatus, fields: MessageInitShape<typeof BuildSchema> = {}) {
	return create(BuildSchema, { id: BUILD_ID, status, createdAt: msToTimestamp(CREATED_MS), ...fields });
}

function deployment(id: string, buildId: string | undefined, createdMs: number) {
	return create(DeploymentSchema, {
		id,
		createdAt: msToTimestamp(createdMs),
		spec: {
			spec: {
				case: "service",
				value: { build: { type: buildId === undefined ? "image" : "dockerfile", image: "x", buildId } },
			},
		},
	});
}

describe("builds", () => {
	test("allows cancel only before a build finishes", () => {
		expect(canCancelBuild(BuildStatus.AWAITING_UPLOAD)).toBe(true);
		expect(canCancelBuild(BuildStatus.QUEUED)).toBe(true);
		expect(canCancelBuild(BuildStatus.RUNNING)).toBe(true);
		expect(canCancelBuild(BuildStatus.SUCCEEDED)).toBe(false);
		expect(canCancelBuild(BuildStatus.FAILED)).toBe(false);
		expect(canCancelBuild(BuildStatus.CANCELED)).toBe(false);
	});

	test("measures took from start to finish with seconds", () => {
		const done = build(BuildStatus.SUCCEEDED, {
			startedAt: msToTimestamp(CREATED_MS),
			finishedAt: msToTimestamp(CREATED_MS + 108_000),
		});
		const took = buildTookMs(done);
		expect(took).toBe(108_000);
		expect(formatBuildDuration(took ?? 0)).toBe("1m 48s");
		expect(formatBuildDuration(38_000)).toBe("38s");
		expect(formatBuildDuration(62 * MINUTE_MS)).toBe("1h 2m");
		expect(buildTookMs(build(BuildStatus.RUNNING, { startedAt: msToTimestamp(CREATED_MS) }))).toBeUndefined();
	});

	test("labels the image by digest, pending or missing", () => {
		expect(buildImageLabel(build(BuildStatus.SUCCEEDED, { imageDigest: DIGEST }))).toEqual({
			text: "sha256:7c41e9b2d6f0",
			muted: false,
		});
		expect(buildImageLabel(build(BuildStatus.RUNNING))).toEqual({ text: "pending", muted: true });
		expect(buildImageLabel(build(BuildStatus.FAILED))).toEqual({ text: "no image", muted: true });
	});

	test("shows the message only for failed and canceled builds", () => {
		expect(buildMessage(build(BuildStatus.FAILED, { message: "exit code 1" }))).toBe("exit code 1");
		expect(buildMessage(build(BuildStatus.CANCELED, { message: "canceled" }))).toBe("canceled");
		expect(buildMessage(build(BuildStatus.SUCCEEDED, { message: "pushed" }))).toBeUndefined();
		expect(buildMessage(build(BuildStatus.FAILED))).toBeUndefined();
	});

	test("waits for the upload, tails running builds and queries finished ones", () => {
		expect(buildLogSource(build(BuildStatus.AWAITING_UPLOAD))).toEqual({ kind: "none" });
		expect(emptyBuildLogText(BuildStatus.AWAITING_UPLOAD)).toBe("Waiting for the CLI to upload source.");
		expect(buildLogSource(build(BuildStatus.QUEUED))).toEqual({ kind: "tail", sinceMs: CREATED_MS });
		expect(buildLogSource(build(BuildStatus.RUNNING))).toEqual({ kind: "tail", sinceMs: CREATED_MS });
		const finished = CREATED_MS + 2 * MINUTE_MS;
		expect(buildLogSource(build(BuildStatus.FAILED, { finishedAt: msToTimestamp(finished) }))).toEqual({
			kind: "query",
			startMs: CREATED_MS,
			endMs: finished + MINUTE_MS,
		});
	});

	test("clamps a long finished build's log window to the query maximum", () => {
		const finished = CREATED_MS + 2 * QUERY_MAX_MS;
		const source = buildLogSource(build(BuildStatus.SUCCEEDED, { finishedAt: msToTimestamp(finished) }));
		expect(source).toEqual({
			kind: "query",
			startMs: finished + MINUTE_MS - QUERY_MAX_MS,
			endMs: finished + MINUTE_MS,
		});
	});

	test("joins each build to its newest deployment", () => {
		const older = { dep: deployment("dep-old", BUILD_ID, CREATED_MS) };
		const newer = { dep: deployment("dep-new", BUILD_ID, CREATED_MS + MINUTE_MS) };
		const image = { dep: deployment("dep-image", undefined, CREATED_MS + 2 * MINUTE_MS) };
		const joined = deploymentsByBuild([older, image, newer]);
		expect(joined.get(BUILD_ID)).toBe(newer);
		expect(joined.size).toBe(1);
	});

	test("finds the page that holds a row", () => {
		expect(pageOf(0, 5)).toBe(0);
		expect(pageOf(4, 5)).toBe(0);
		expect(pageOf(5, 5)).toBe(1);
	});
});
