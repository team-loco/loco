import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { BuildSchema, BuildStatus } from "@gen/loco/build/v1/build_pb";
import { DeploymentActorType, DeploymentMethod } from "@gen/loco/deployment/v1/deployment_pb";

import { DAY_MS } from "@/lib/time";
import { msToTimestamp } from "@/lib/time";

import {
	buildImageLabel,
	buildLogSource,
	buildMessage,
	buildStarter,
	buildTookMs,
	canCancelBuild,
	emptyBuildLogText,
	formatBuildDuration,
} from "./builds";

const BUILD_ID = "0199d1c2-0000-7000-8000-00000000b001";
const DIGEST = "sha256:7c41e9b2d6f0a18c4e00000000000000000000000000000000000000000000ff";
const CREATED_MS = 1_760_000_000_000;
const MINUTE_MS = 60_000;

function build(status: BuildStatus, fields: MessageInitShape<typeof BuildSchema> = {}) {
	return create(BuildSchema, { id: BUILD_ID, status, createdAt: msToTimestamp(CREATED_MS), ...fields });
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
		expect(buildLogSource(build(BuildStatus.AWAITING_UPLOAD), DAY_MS)).toEqual({ kind: "none" });
		expect(emptyBuildLogText(BuildStatus.AWAITING_UPLOAD)).toBe("Waiting for the CLI to upload source.");
		expect(buildLogSource(build(BuildStatus.QUEUED), DAY_MS)).toEqual({ kind: "tail", sinceMs: CREATED_MS });
		expect(buildLogSource(build(BuildStatus.RUNNING), DAY_MS)).toEqual({ kind: "tail", sinceMs: CREATED_MS });
		const finished = CREATED_MS + 2 * MINUTE_MS;
		expect(buildLogSource(build(BuildStatus.FAILED, { finishedAt: msToTimestamp(finished) }), DAY_MS)).toEqual({
			kind: "query",
			startMs: CREATED_MS,
			endMs: finished + MINUTE_MS,
		});
	});

	test("clamps a long finished build's log window to the query maximum", () => {
		const finished = CREATED_MS + 2 * DAY_MS;
		const source = buildLogSource(build(BuildStatus.SUCCEEDED, { finishedAt: msToTimestamp(finished) }), DAY_MS / 4);
		expect(source).toEqual({
			kind: "query",
			startMs: finished + MINUTE_MS - DAY_MS / 4,
			endMs: finished + MINUTE_MS,
		});
	});
});

describe("buildStarter", () => {
	test("names a member and the client that started the build", () => {
		const starter = buildStarter(
			build(BuildStatus.SUCCEEDED, { actorType: DeploymentActorType.USER, method: DeploymentMethod.CLI }),
			{ name: "Ada", email: "ada@example.com" },
		);
		expect(starter).toEqual({ name: "Ada", detail: "loco deploy · CLI" });
	});

	test("labels a token build as an API token", () => {
		const starter = buildStarter(
			build(BuildStatus.SUCCEEDED, {
				actorType: DeploymentActorType.API_TOKEN,
				method: DeploymentMethod.API_TOKEN,
				createdBy: "0199d1c2-0000-7000-8000-00000000a001",
			}),
			undefined,
		);
		expect(starter.name).toBe("API token · 0000a001");
	});

	test("names a token build after the token while it exists", () => {
		const starter = buildStarter(
			build(BuildStatus.SUCCEEDED, {
				actorType: DeploymentActorType.API_TOKEN,
				method: DeploymentMethod.API_TOKEN,
				actorName: "ci",
			}),
			undefined,
		);
		expect(starter).toEqual({ name: "ci", detail: "API token" });
	});
});
