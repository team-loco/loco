import { useSyncExternalStore } from "react";

import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

export const CPU_STOPS = ["100m", "250m", "500m", "1", "2"];
export const MEMORY_STOPS = ["128Mi", "256Mi", "512Mi", "1Gi", "2Gi", "4Gi"];
export const MAX_REPLICAS = 3;

export const DRAFT_DEFAULTS = {
	cpu: "250m",
	memory: "256Mi",
	min: 1,
	max: 1,
	port: "8000",
	cpuTarget: "70",
	hcPath: "/health",
	hcInterval: "30",
	hcTimeout: "5",
	hcFail: "3",
};

export type Networking = "public" | "private";

export function isPrivate(draft: Draft): boolean {
	return draft.networking === "private";
}

export interface DraftVar {
	key: string;
	value: string;
}

export interface Draft {
	id: string;
	name: string;
	image: string;
	region: string;
	sub: string;
	networking?: Networking | undefined;
	port: string;
	cpu: string;
	memory: string;
	min: number;
	max: number;
	cpuTarget: string;
	vars: DraftVar[];
	hcPath: string;
	hcInterval: string;
	hcTimeout: string;
	hcFail: string;
	resourceId?: string | undefined;
	deploymentId?: string | undefined;
	deployedAt?: number | undefined;
}

const IMAGE_RE =
	/^([a-z0-9][a-z0-9.\-_]*(:[0-9]+)?\/)?[a-z0-9._\-/]+(:[A-Za-z0-9._-]+(@sha256:[a-f0-9]{64})?|@sha256:[a-f0-9]{64})$/;
const SERVER_IMAGE_RE = /^([a-z0-9\-._]+(:[0-9]+)?(\/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+)?(@sha256:[a-f0-9]{64})?$/;
export const NAME_RE = /^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$/;

export function imageError(image: string): string | null {
	if (image === "") return null;
	if (!IMAGE_RE.test(image) || !SERVER_IMAGE_RE.test(image)) {
		return "Use registry/repository:tag or an @sha256 digest";
	}
	return null;
}

export function nameFromImage(image: string): string {
	const last = image.trim().split("/").pop() ?? "";
	const repo = last.split(/[:@]/)[0] ?? "";
	return repo
		.toLowerCase()
		.replace(/[^a-z0-9-]/g, "-")
		.replace(/^[^a-z]+|-+$/g, "")
		.slice(0, 63);
}

export function uniqueName(base: string, taken: Set<string>): string {
	const root = base === "" ? "service" : base;
	let name = root;
	for (let i = 2; taken.has(name); i++) name = `${root}-${i.toString()}`;
	return name;
}

const listeners = new Set<() => void>();
const cache = new Map<string, { raw: string | null; drafts: Draft[] }>();
const EMPTY: Draft[] = [];

interface DraftKeys {
	key: string;
	legacy: string;
}

function draftKeys(workspaceId: string, envId: string): DraftKeys {
	return {
		key: `loco:drafts:v1:${workspaceId}:${envId}`,
		legacy: `loco_drafts_${workspaceId}_${envId}`,
	};
}

function readRaw(keys: DraftKeys): string | null {
	return readStorage(keys.key) ?? readStorage(keys.legacy);
}

function isDraft(v: unknown): v is Draft {
	if (typeof v !== "object" || v === null) return false;
	return "id" in v && "name" in v && "image" in v && "vars" in v;
}

function readDrafts(keys: DraftKeys): Draft[] {
	const raw = readRaw(keys);
	const hit = cache.get(keys.key);
	if (hit?.raw === raw) return hit.drafts;
	let drafts: Draft[] = EMPTY;
	if (raw !== null) {
		try {
			const parsed: unknown = JSON.parse(raw);
			drafts = Array.isArray(parsed) ? parsed.filter(isDraft) : EMPTY;
		} catch {
			drafts = EMPTY;
		}
	}
	cache.set(keys.key, { raw, drafts });
	return drafts;
}

function notify() {
	listeners.forEach((l) => {
		l();
	});
}

function writeDrafts(keys: DraftKeys, drafts: Draft[]) {
	if (writeStorage(keys.key, JSON.stringify(drafts))) {
		removeStorage(keys.legacy);
	} else {
		cache.set(keys.key, { raw: readRaw(keys), drafts });
	}
	notify();
}

function subscribe(listener: () => void) {
	if (listeners.size === 0) window.addEventListener("storage", notify);
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
		if (listeners.size === 0) window.removeEventListener("storage", notify);
	};
}

export function useDrafts(workspaceId: string | null, envId: string | undefined) {
	const key = workspaceId !== null && envId !== undefined ? draftKeys(workspaceId, envId) : null;
	const drafts = useSyncExternalStore(
		subscribe,
		() => (key === null ? EMPTY : readDrafts(key)),
		() => EMPTY,
	);
	const save = (next: Draft[]) => {
		if (key !== null) writeDrafts(key, next);
	};
	return {
		drafts,
		add: (d: Draft) => {
			const current = key === null ? EMPTY : readDrafts(key);
			save([...current, d]);
		},
		update: (id: string, patch: Partial<Draft>) => {
			const current = key === null ? EMPTY : readDrafts(key);
			save(current.map((d) => (d.id === id ? { ...d, ...patch } : d)));
		},
		remove: (id: string) => {
			const current = key === null ? EMPTY : readDrafts(key);
			save(current.filter((d) => d.id !== id));
		},
	};
}
