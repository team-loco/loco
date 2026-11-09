import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

export type PodPhase = "starting" | "ready" | "terminating";

export interface Pod {
	id: string;
	fresh: boolean;
	phase: PodPhase;
}

export type LineTone = "command" | "output" | "ok" | "link";

export interface TerminalLine {
	at: number;
	text: string;
	tone: LineTone;
}

export interface RegionFrame {
	name: string;
	pods: readonly Pod[];
}

export interface DemoFrame {
	shown: number;
	status: ResourceStatus;
	ready: number;
	desired: number;
	regions: readonly RegionFrame[];
}

const SURGE_PERCENT = 25;
const REPLICAS = 4;
const BUILD_ID = "019a6b2e-41c7-7d3a-9f02-5b8e1c4d7a90";

export const ENDPOINT = "api-kwvn.onloco.app";
export const STRATEGY = `Rolling · ${SURGE_PERCENT}% surge · ${SURGE_PERCENT}% unavailable`;

const DEPLOYED_AT = 17;
const STAGE_FRAMES = 2;
const REGION_STARTS = [
	{ name: "us-east-1", start: 19 },
	{ name: "eu-west-1", start: 20 },
] as const;

export const TERMINAL: readonly TerminalLine[] = [
	{ at: 0, text: "$ loco deploy --yes", tone: "command" },
	{ at: 2, text: "Plan for loco.yaml against environment revision 7.", tone: "output" },
	{ at: 3, text: "Update", tone: "output" },
	{ at: 3, text: "  ~ api  (needs deploy)", tone: "output" },
	{ at: 3, text: "Plan: 1 to update.", tone: "output" },
	{ at: 5, text: "Packed 214 files from . (1.8 MiB compressed)", tone: "output" },
	{ at: 6, text: "Building api from Dockerfile in .", tone: "output" },
	{ at: 7, text: `Uploading the source for build ${BUILD_ID}`, tone: "output" },
	{ at: 8, text: `Build ${BUILD_ID} running`, tone: "output" },
	{
		at: 16,
		text: `Build ${BUILD_ID} succeeded in 38s: registry.onloco.app/ws-019a1f04-8c2d-7e55-b6a1-0d9e3c7f2a18/019a1f05-3b6e-7a90-8d24-6f1c0e9b5d73@sha256:4be1c09d`,
		tone: "ok",
	},
	{ at: DEPLOYED_AT, text: "Applied. Environment revision is now 8.", tone: "ok" },
	{
		at: DEPLOYED_AT,
		text: "Started deployment 019a6b31-0f8e-7c12-a4d5-2e7b9c3f6a01 for api in us-east-1.",
		tone: "output",
	},
	{
		at: DEPLOYED_AT,
		text: "Started deployment 019a6b31-0f8e-7c12-a4d5-2e7b9c3f6a02 for api in eu-west-1.",
		tone: "output",
	},
	{ at: 18, text: `api: https://${ENDPOINT}`, tone: "link" },
	{ at: 19, text: "$", tone: "command" },
];

function pod(n: number, fresh: boolean, phase: PodPhase): Pod {
	return { id: `p${n.toString()}`, fresh, phase };
}

const STAGES: readonly (readonly Pod[])[] = [
	[pod(1, false, "ready"), pod(2, false, "ready"), pod(3, false, "ready"), pod(4, false, "ready")],
	[
		pod(1, false, "terminating"),
		pod(2, false, "ready"),
		pod(3, false, "ready"),
		pod(4, false, "ready"),
		pod(5, true, "starting"),
		pod(6, true, "starting"),
	],
	[
		pod(2, false, "terminating"),
		pod(3, false, "terminating"),
		pod(4, false, "ready"),
		pod(5, true, "ready"),
		pod(6, true, "ready"),
		pod(7, true, "starting"),
		pod(8, true, "starting"),
	],
	[
		pod(4, false, "terminating"),
		pod(5, true, "ready"),
		pod(6, true, "ready"),
		pod(7, true, "ready"),
		pod(8, true, "ready"),
	],
	[pod(5, true, "ready"), pod(6, true, "ready"), pod(7, true, "ready"), pod(8, true, "ready")],
];
const LAST_STAGE = STAGES.length - 1;
const LAST_START = Math.max(...REGION_STARTS.map((r) => r.start));

export const REST_FRAME = LAST_START + LAST_STAGE * STAGE_FRAMES;

function stageAt(frame: number, start: number): readonly Pod[] {
	const index = frame < start ? 0 : Math.min(Math.floor((frame - start) / STAGE_FRAMES) + 1, LAST_STAGE);
	return STAGES[index] ?? [];
}

export function demoFrame(frame: number): DemoFrame {
	const regions = REGION_STARTS.map(({ name, start }) => ({ name, pods: stageAt(frame, start) }));
	const ready = regions.reduce((sum, r) => sum + r.pods.filter((p) => p.phase === "ready").length, 0);
	const deploying = frame >= DEPLOYED_AT && frame < REST_FRAME;
	return {
		shown: TERMINAL.filter((line) => line.at <= frame).length,
		status: deploying ? ResourceStatus.DEPLOYING : ResourceStatus.HEALTHY,
		ready,
		desired: REPLICAS * regions.length,
		regions,
	};
}
