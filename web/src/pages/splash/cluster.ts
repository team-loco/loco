export type PodPhase = "ready" | "starting" | "terminating";

export interface Pod {
	id: string;
	tag: string;
	phase: PodPhase;
}

export interface Rollout {
	pods: Pod[];
	target: number;
	tag: string;
	rolling: boolean;
}

export type MakePod = (tag: string, phase: PodPhase) => Pod;

export const MIN_REPLICAS = 1;
export const MAX_REPLICAS = 6;
const FIRST_TAG = "7c41e9";
const SECOND_TAG = "8a03f2";

export function initialRollout(): Rollout {
	const pods = [100, 101, 102].map((n) => ({ id: `p${n}`, tag: FIRST_TAG, phase: "ready" as const }));
	return { pods, target: 3, tag: FIRST_TAG, rolling: false };
}

export function nextTag(tag: string): string {
	return tag === FIRST_TAG ? SECOND_TAG : FIRST_TAG;
}

export function readyCount(rollout: Rollout): number {
	return rollout.pods.filter((p) => p.phase === "ready" && p.tag === rollout.tag).length;
}

export function rollStep(rollout: Rollout, makePod: MakePod): { rollout: Rollout; delay: number | null } {
	const { pods, tag, target } = rollout;
	const starting = pods.find((p) => p.phase === "starting");
	if (starting) {
		const next = pods.map((p) => (p.id === starting.id ? { ...p, phase: "ready" as const } : p));
		return { rollout: { ...rollout, pods: next }, delay: 520 };
	}
	const terminating = pods.find((p) => p.phase === "terminating");
	if (terminating) {
		return { rollout: { ...rollout, pods: pods.filter((p) => p.id !== terminating.id) }, delay: 360 };
	}
	const fresh = pods.filter((p) => p.tag === tag).length;
	if (fresh < target) {
		const pod = makePod(tag, "starting");
		return { rollout: { ...rollout, pods: [...pods, pod] }, delay: 900 };
	}
	const stale = pods.find((p) => p.tag !== tag);
	if (stale) {
		const next = pods.map((p) => (p.id === stale.id ? { ...p, phase: "terminating" as const } : p));
		return { rollout: { ...rollout, pods: next }, delay: 700 };
	}
	return { rollout: { ...rollout, rolling: false }, delay: null };
}

export function scaleTo(rollout: Rollout, target: number, makePod: MakePod): Rollout {
	const live = rollout.pods.filter((p) => p.phase !== "terminating");
	const added = Array.from({ length: Math.max(0, target - live.length) }, () => makePod(rollout.tag, "starting"));
	const pods = [...live, ...added].map((p, i) => (i >= target ? { ...p, phase: "terminating" as const } : p));
	return { ...rollout, pods, target, rolling: true };
}

export function settle(rollout: Rollout): Rollout {
	const pods = rollout.pods.filter((p) => p.phase !== "terminating").map((p) => ({ ...p, phase: "ready" as const }));
	return { ...rollout, pods, rolling: false };
}

export const VIEW_W = 1000;
export const VIEW_H = 470;

export interface Box {
	x: number;
	y: number;
	w: number;
	h: number;
}

export const BOX = {
	client: { x: 10, y: 196, w: 136, h: 78 },
	cluster: { x: 214, y: 22, w: 770, h: 426 },
	gateway: { x: 240, y: 192, w: 136, h: 86 },
	ns: { x: 426, y: 58, w: 546, h: 372 },
	web: { x: 458, y: 88, w: 200, h: 118 },
	api: { x: 458, y: 276, w: 200, h: 118 },
	worker: { x: 750, y: 88, w: 200, h: 118 },
	db: { x: 750, y: 276, w: 200, h: 118 },
} satisfies Record<string, Box>;

type Point = readonly [number, number];

function leftOf(b: Box): Point {
	return [b.x, b.y + b.h / 2];
}

function rightOf(b: Box): Point {
	return [b.x + b.w, b.y + b.h / 2];
}

function ortho(a: Point, b: Point, r = 10): string {
	const [x1, y1] = a;
	const [x2, y2] = b;
	if (y1 === y2) return `M${x1},${y1} L${x2},${y2}`;
	const mx = (x1 + x2) / 2;
	const s = y2 > y1 ? 1 : -1;
	return `M${x1},${y1} L${mx - r},${y1} Q${mx},${y1} ${mx},${y1 + s * r} L${mx},${y2 - s * r} Q${mx},${y2} ${mx + r},${y2} L${x2},${y2}`;
}

export type NodeKey = "in" | "gw" | "web" | "api" | "wk" | "db";

export interface Edge {
	id: string;
	ends: readonly NodeKey[];
	d: string;
	label: string;
	lx: number;
	ly: number;
	packet: boolean;
}

const workerDb = `M${BOX.worker.x + BOX.worker.w / 2},${BOX.worker.y + BOX.worker.h} L${BOX.db.x + BOX.db.w / 2},${BOX.db.y}`;

export const EDGES: readonly Edge[] = [
	{ id: "in", ends: ["in"], d: ortho(rightOf(BOX.client), leftOf(BOX.gateway)), label: ":443", lx: 180, ly: 221, packet: true },
	{ id: "gw-web", ends: ["gw", "web"], d: ortho(rightOf(BOX.gateway), leftOf(BOX.web)), label: "/", lx: 417, ly: 135, packet: true },
	{ id: "gw-api", ends: ["gw", "api"], d: ortho(rightOf(BOX.gateway), leftOf(BOX.api)), label: "/api", lx: 417, ly: 323, packet: true },
	{ id: "api-db", ends: ["api", "db"], d: ortho(rightOf(BOX.api), leftOf(BOX.db)), label: ":5432", lx: 704, ly: 321, packet: true },
	{ id: "wk-db", ends: ["wk", "db"], d: workerDb, label: ":5432", lx: 880, ly: 241, packet: false },
];

export function pctX(v: number): string {
	return `${(v / VIEW_W) * 100}%`;
}

export function pctY(v: number): string {
	return `${(v / VIEW_H) * 100}%`;
}
