import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { DeploymentActorType, DeploymentSchema, DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { EventSchema } from "@gen/loco/event/v1/event_pb";

import { degradedToast, deployToasts } from "./deploy-toasts";

const NAMES = new Map([["r1", "api"]]);
const OPENED = 1_800_000_000_000;

function deployment(status: DeploymentStatus, problem?: { reason: string; message: string }, actorId = "u-ada") {
	return create(DeploymentSchema, {
		id: "d1",
		resourceId: "r1",
		status,
		actorType: DeploymentActorType.USER,
		actorId,
		actorName: "Ada",
		commit: { sha: "3e8a51d0c1" },
		createdAt: timestampFromMs(OPENED + 1000),
		updatedAt: timestampFromMs(OPENED + 1000),
		placements: [{ region: "us-va-1", ...(problem === undefined ? {} : { problem }) }],
	});
}

/** The toast tones, titles and descriptions as a deployment changing after the shell opened moves through `steps`. */
function titles(steps: ReturnType<typeof deployment>[], viewerId = "u-me"): string[][] {
	let seen = new Map<string, string>();
	const out: string[][] = [];
	for (const d of steps) {
		const next = deployToasts(seen, [d], NAMES, viewerId, OPENED);
		seen = next.seen;
		out.push(next.toasts.map((t) => `${t.tone}: ${t.title} (${t.description})`));
	}
	return out;
}

test("a deployment that last changed before the shell opened toasts only once it changes", () => {
	const before = timestampFromMs(OPENED - 1000);
	const old = { ...deployment(DeploymentStatus.DEPLOYING), createdAt: before, updatedAt: before };
	const first = deployToasts(new Map(), [old], NAMES, "u-me", OPENED);
	expect(first.toasts).toEqual([]);
	const failed = { ...old, status: DeploymentStatus.FAILED };
	expect(deployToasts(first.seen, [failed], NAMES, "u-me", OPENED).toasts.map((t) => t.title)).toEqual([
		"api deploy failed",
	]);
});

test("a deployment started before the shell opened that fails after it toasts on first sight", () => {
	const failed = {
		...deployment(DeploymentStatus.FAILED, { reason: "image_pull_failed", message: "Pull failed." }),
		createdAt: timestampFromMs(OPENED - 60_000),
	};
	expect(deployToasts(new Map(), [failed], NAMES, "u-me", OPENED).toasts.map((t) => t.title)).toEqual([
		"api deploy failed",
	]);
});

test("a new resource's first deploy toasts once its name loads", () => {
	const d = deployment(DeploymentStatus.DEPLOYING);
	const unnamed = deployToasts(new Map(), [d], new Map(), "u-me", OPENED);
	expect(unnamed.toasts).toEqual([]);
	expect(deployToasts(unnamed.seen, [d], NAMES, "u-me", OPENED).toasts.map((t) => t.title)).toEqual([
		"api is deploying",
	]);
});

test("a degraded event toasts the problem, whatever the deployment's age", () => {
	const degraded = (reason: string) =>
		create(EventSchema, {
			id: "e1",
			type: "resource.degraded",
			resourceId: "r1",
			deploymentId: "d-yesterday",
			data: { reason, message: "The service ran out of memory." },
		});
	expect(degradedToast(degraded("out_of_memory"), NAMES)).toEqual({
		deploymentId: "d-yesterday",
		resourceId: "r1",
		tone: "warning",
		title: "api: out of memory",
		description: "The service ran out of memory.",
	});
	expect(degradedToast(degraded("something_new"), NAMES)?.title).toBe("api has a problem");
	expect(degradedToast(degraded("out_of_memory"), new Map())).toBeNull();
	expect(degradedToast({ ...degraded("out_of_memory"), type: "resource.recovered" }, NAMES)).toBeNull();
});

test("a deploy toasts when it starts, fails and retries, and goes live, once per change; a live problem leaves it live", () => {
	const oom = { reason: "out_of_memory", message: "The service ran out of memory." };
	expect(
		titles([
			deployment(DeploymentStatus.BUILDING),
			deployment(DeploymentStatus.DEPLOYING),
			deployment(DeploymentStatus.DEPLOYING, { reason: "image_pull_failed", message: "Pull failed." }),
			deployment(DeploymentStatus.RUNNING),
			deployment(DeploymentStatus.RUNNING, oom),
			deployment(DeploymentStatus.SUPERSEDED),
		]),
	).toEqual([
		["deploying: api is deploying (Ada, 3e8a51d)"],
		[],
		["warning: api deploy is failing (Pull failed.)"],
		["live: api is live on 3e8a51d ()"],
		[],
		[],
	]);
});

test("a failed deploy toasts failed", () => {
	const crash = { reason: "restarts_exhausted", message: "Crashed." };
	expect(titles([deployment(DeploymentStatus.CRASHED, crash)])).toEqual([["failed: api deploy failed (Crashed.)"]]);
});

test("the viewer's own deploy skips the start toast, and deploys of unknown resources toast nothing", () => {
	expect(titles([deployment(DeploymentStatus.DEPLOYING, undefined, "u-me")])).toEqual([[]]);
	const other = { ...deployment(DeploymentStatus.FAILED), resourceId: "gone" };
	expect(deployToasts(new Map(), [other], NAMES, "u-me", OPENED).toasts).toEqual([]);
});
