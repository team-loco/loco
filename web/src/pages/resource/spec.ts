import { toJson, type JsonObject, type JsonValue } from "@bufbuild/protobuf";
import { DeploymentSpecSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

function isObject(v: JsonValue | undefined): v is JsonObject {
	return typeof v === "object" && v !== null && !Array.isArray(v);
}

function sortKeys(obj: JsonObject): JsonObject {
	const out: JsonObject = {};
	for (const k of Object.keys(obj).sort()) {
		const v = obj[k];
		if (v !== undefined) out[k] = v;
	}
	return out;
}

function normalize(value: JsonValue): JsonValue {
	if (Array.isArray(value)) return value.map(normalize);
	if (!isObject(value)) return value;
	const out: JsonObject = {};
	for (const [k, v] of Object.entries(value)) {
		out[k] = k === "env" && isObject(v) ? sortKeys(v) : normalize(v);
	}
	return out;
}

function specObject(dep: Deployment): JsonObject {
	const json = dep.spec === undefined ? {} : toJson(DeploymentSpecSchema, dep.spec);
	const base = isObject(json) ? json : {};
	const normalized = normalize({ ...base, region: dep.region, replicas: dep.replicas });
	return isObject(normalized) ? normalized : {};
}

export function specText(dep: Deployment): string {
	return JSON.stringify(specObject(dep), null, 2);
}

export interface DiffLine {
	sign: " " | "+" | "−";
	text: string;
}

export function diffLines(from: string, to: string): { lines: DiffLine[]; add: number; del: number } {
	const a = from.split("\n");
	const b = to.split("\n");
	const n = a.length;
	const m = b.length;
	const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
	const at = (i: number, j: number): number => lcs[i]?.[j] ?? 0;
	for (let i = n - 1; i >= 0; i--) {
		const row = lcs[i];
		if (row === undefined) continue;
		for (let j = m - 1; j >= 0; j--) {
			row[j] = a[i] === b[j] ? at(i + 1, j + 1) + 1 : Math.max(at(i + 1, j), at(i, j + 1));
		}
	}
	const lines: DiffLine[] = [];
	let i = 0;
	let j = 0;
	let add = 0;
	let del = 0;
	while (i < n && j < m) {
		const ai = a[i] ?? "";
		const bj = b[j] ?? "";
		if (ai === bj) {
			lines.push({ sign: " ", text: ai });
			i++;
			j++;
		} else if (at(i + 1, j) >= at(i, j + 1)) {
			lines.push({ sign: "−", text: ai });
			i++;
			del++;
		} else {
			lines.push({ sign: "+", text: bj });
			j++;
			add++;
		}
	}
	while (i < n) {
		lines.push({ sign: "−", text: a[i] ?? "" });
		i++;
		del++;
	}
	while (j < m) {
		lines.push({ sign: "+", text: b[j] ?? "" });
		j++;
		add++;
	}
	return { lines, add, del };
}
