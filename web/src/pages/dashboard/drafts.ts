import { useSyncExternalStore } from "react";

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

export const IMAGE_RE =
	/^([a-z0-9][a-z0-9.\-_]*(:[0-9]+)?\/)?[a-z0-9._\-/]+(:[A-Za-z0-9._-]+|@sha256:[a-f0-9]{64})$/;
const SERVER_IMAGE_RE = /^([a-z0-9\-._]+(\/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+|@sha256:[a-f0-9]{64})?$/;
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

export function parseDotEnv(text: string): DraftVar[] {
	const out: DraftVar[] = [];
	for (const raw of text.split(/\r?\n/)) {
		const line = raw.trim().replace(/^export\s+/, "");
		if (line === "" || line.startsWith("#")) continue;
		const eq = line.indexOf("=");
		if (eq < 1) continue;
		const key = line.slice(0, eq).trim();
		let value = line.slice(eq + 1).trim();
		const quoted =
			(value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"));
		if (quoted && value.length >= 2) value = value.slice(1, -1);
		if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) out.push({ key, value });
	}
	return out;
}

const listeners = new Set<() => void>();
const cache = new Map<string, { raw: string | null; drafts: Draft[] }>();
const EMPTY: Draft[] = [];

function storageKey(workspaceId: string, envId: string): string {
	return `loco_drafts_${workspaceId}_${envId}`;
}

function readRaw(key: string): string | null {
	try {
		return localStorage.getItem(key);
	} catch {
		return null;
	}
}

function isDraft(v: unknown): v is Draft {
	if (typeof v !== "object" || v === null) return false;
	return "id" in v && "name" in v && "image" in v && "vars" in v;
}

function readDrafts(key: string): Draft[] {
	const raw = readRaw(key);
	const hit = cache.get(key);
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
	cache.set(key, { raw, drafts });
	return drafts;
}

function writeDrafts(key: string, drafts: Draft[]) {
	const raw = JSON.stringify(drafts);
	try {
		localStorage.setItem(key, raw);
	} catch {
		cache.set(key, { raw: readRaw(key), drafts });
	}
	listeners.forEach((l) => {
		l();
	});
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	const onStorage = () => {
		listener();
	};
	window.addEventListener("storage", onStorage);
	return () => {
		listeners.delete(listener);
		window.removeEventListener("storage", onStorage);
	};
}

export function useDrafts(workspaceId: string | null, envId: string | undefined) {
	const key = workspaceId !== null && envId !== undefined ? storageKey(workspaceId, envId) : null;
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
			save([...drafts, d]);
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
