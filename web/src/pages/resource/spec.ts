import { toJson, type JsonObject, type JsonValue } from "@bufbuild/protobuf";
import { DeploymentSpecSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { getServiceSpec } from "@/lib/deployment-utils";
import { shortImageRef } from "@/lib/image";

function isObject(v: JsonValue | undefined): v is JsonObject {
	return typeof v === "object" && v !== null && !Array.isArray(v);
}

function sortKeys(obj: JsonObject): JsonObject {
	const out: JsonObject = {};
	for (const k of Object.keys(obj).toSorted()) {
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
	const normalized = normalize({
		...base,
		region: dep.region,
		replicas: dep.replicas,
	});
	return isObject(normalized) ? normalized : {};
}

export function specText(dep: Deployment): string {
	return JSON.stringify(specObject(dep), null, 2);
}

interface DiffLine {
	sign: " " | "+" | "−";
	text: string;
}

export function diffLines(from: string, to: string): { lines: DiffLine[]; add: number; del: number } {
	const a = from.split("\n");
	const b = to.split("\n");
	const n = a.length;
	const m = b.length;
	const lcs: number[][] = Array.from({ length: n + 1 }, () => Array.from({ length: m + 1 }, () => 0));
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

interface SideBySideRow {
	left: { n: number; text: string; changed: boolean } | null;
	right: { n: number; text: string; changed: boolean } | null;
}

export function sideBySide(lines: DiffLine[]): SideBySideRow[] {
	const rows: SideBySideRow[] = [];
	let leftN = 0;
	let rightN = 0;
	let i = 0;
	while (i < lines.length) {
		const line = lines[i];
		if (line === undefined) break;
		if (line.sign === " ") {
			leftN++;
			rightN++;
			rows.push({
				left: { n: leftN, text: line.text, changed: false },
				right: { n: rightN, text: line.text, changed: false },
			});
			i++;
			continue;
		}
		const removed: string[] = [];
		const added: string[] = [];
		while (i < lines.length && lines[i]?.sign !== " ") {
			const next = lines[i];
			if (next?.sign === "−") removed.push(next.text);
			if (next?.sign === "+") added.push(next.text);
			i++;
		}
		for (let k = 0; k < Math.max(removed.length, added.length); k++) {
			const l = removed[k];
			const r = added[k];
			rows.push({
				left: l === undefined ? null : { n: ++leftN, text: l, changed: true },
				right: r === undefined ? null : { n: ++rightN, text: r, changed: true },
			});
		}
	}
	return rows;
}

interface SpecChange {
	label: string;
	from: string | null;
	to: string | null;
}

export const ABSENT = "—";

function healthLine(dep: Deployment): string | null {
	const hc = getServiceSpec(dep)?.healthCheck;
	if (hc === undefined) return null;
	return `${hc.path} every ${hc.intervalSeconds.toString()}s`;
}

export function specChanges(from: Deployment, to: Deployment): SpecChange[] {
	const f = getServiceSpec(from);
	const t = getServiceSpec(to);
	const out: SpecChange[] = [];
	const push = (label: string, a: string | null, b: string | null) => {
		if (a !== b) out.push({ label, from: a, to: b });
	};
	const fromImage = f?.build?.image ?? "";
	const toImage = t?.build?.image ?? "";
	if (fromImage !== toImage) {
		out.push({
			label: "image",
			from: shortImageRef(fromImage) || null,
			to: shortImageRef(toImage) || null,
		});
	}
	push("replicas", from.replicas.toString(), to.replicas.toString());
	push("cpu", f?.cpu ?? null, t?.cpu ?? null);
	push("memory", f?.memory ?? null, t?.memory ?? null);
	push("port", f?.port.toString() ?? null, t?.port.toString() ?? null);
	push("health", healthLine(from), healthLine(to));
	const fEnv = f?.env ?? {};
	const tEnv = t?.env ?? {};
	const keys = [...new Set([...Object.keys(fEnv), ...Object.keys(tEnv)])].toSorted();
	for (const k of keys) push(`env.${k}`, fEnv[k] ?? null, tEnv[k] ?? null);
	return out;
}
