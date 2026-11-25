import { toJson, type JsonObject, type JsonValue } from "@bufbuild/protobuf";
import { DeploymentSpecSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { desiredReplicas, getServiceSpec, regionTarget } from "@/lib/deployment-utils";
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
		region: dep.placements[0]?.region ?? "",
		replicas: desiredReplicas(dep),
	});
	return isObject(normalized) ? normalized : {};
}

const PLAIN_SCALAR = /^[\w/.][\w./:@-]*$/;
const YAML_WORDS = new Set(["true", "false", "null", "yes", "no", "on", "off", "~"]);

function yamlScalar(value: string | number | boolean | null): string {
	if (typeof value !== "string") return String(value);
	const plain = PLAIN_SCALAR.test(value) && !YAML_WORDS.has(value.toLowerCase()) && Number.isNaN(Number(value));
	return plain ? value : JSON.stringify(value);
}

/** Block-style YAML for a JSON value, keys in their given order. */
export function toYaml(value: JsonValue, indent = ""): string {
	if (Array.isArray(value)) {
		if (value.length === 0) return "[]";
		return value
			.map((v) => {
				const inner = toYaml(v, `${indent}  `);
				return `${indent}- ${isObject(v) || Array.isArray(v) ? inner.trimStart() : inner}`;
			})
			.join("\n");
	}
	if (isObject(value)) {
		const entries = Object.entries(value);
		if (entries.length === 0) return "{}";
		return entries
			.map(([k, v]) => {
				const nested = (isObject(v) && Object.keys(v).length > 0) || (Array.isArray(v) && v.length > 0);
				return nested ? `${indent}${k}:\n${toYaml(v, `${indent}  `)}` : `${indent}${k}: ${toYaml(v, "")}`;
			})
			.join("\n");
	}
	return yamlScalar(value);
}

export function specText(dep: Deployment): string {
	return toYaml(specObject(dep));
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

export interface SideBySideRow {
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

/** A run of unchanged rows left out of a changes-only view. */
export interface SkippedRows {
	skipped: number;
}

function leadingSpaces(cell: SideBySideRow["left"]): number {
	return cell === null ? Infinity : cell.text.length - cell.text.trimStart().length;
}

/** The changed rows with the keys they sit under, and a marker for each run of rows left out. */
export function changedOnly(rows: SideBySideRow[]): (SideBySideRow | SkippedRows)[] {
	const keep = new Set<number>();
	rows.forEach((row, i) => {
		if (row.left?.changed !== true && row.right?.changed !== true) return;
		keep.add(i);
		let level = Math.min(leadingSpaces(row.left), leadingSpaces(row.right));
		for (let j = i - 1; j >= 0 && level > 0; j--) {
			const above = rows[j];
			if (above === undefined || above.left?.changed === true || above.right?.changed === true) continue;
			const d = leadingSpaces(above.left);
			if (d < level) {
				keep.add(j);
				level = d;
			}
		}
	});
	const out: (SideBySideRow | SkippedRows)[] = [];
	let skipped = 0;
	rows.forEach((row, i) => {
		if (!keep.has(i)) {
			skipped++;
			return;
		}
		if (skipped > 0) out.push({ skipped });
		skipped = 0;
		out.push(row);
	});
	if (skipped > 0) out.push({ skipped });
	return out;
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

function entriesLine(entries: string[] | undefined): string | null {
	return entries === undefined || entries.length === 0 ? null : JSON.stringify(entries);
}

export interface SpecRow extends SpecChange {
	same: boolean;
}

/** Every setting the two deployments run with, marking the ones that match. */
export function specRows(from: Deployment, to: Deployment): SpecRow[] {
	const f = getServiceSpec(from);
	const t = getServiceSpec(to);
	const out: SpecRow[] = [];
	const push = (label: string, a: string | null, b: string | null, same = a === b) => {
		out.push({ label, from: a, to: b, same });
	};
	const fromImage = f?.build?.image ?? "";
	const toImage = t?.build?.image ?? "";
	push("image", shortImageRef(fromImage) || null, shortImageRef(toImage) || null, fromImage === toImage);
	push("replicas", desiredReplicas(from).toString(), desiredReplicas(to).toString());
	push("cpu", regionTarget(f)?.cpu ?? null, regionTarget(t)?.cpu ?? null);
	push("memory", regionTarget(f)?.memory ?? null, regionTarget(t)?.memory ?? null);
	push("port", f?.port?.toString() ?? null, t?.port?.toString() ?? null);
	push("health", healthLine(from), healthLine(to));
	push("command", entriesLine(f?.command), entriesLine(t?.command));
	push("args", entriesLine(f?.args), entriesLine(t?.args));
	const fEnv = f?.env ?? {};
	const tEnv = t?.env ?? {};
	const keys = [...new Set([...Object.keys(fEnv), ...Object.keys(tEnv)])].toSorted();
	for (const k of keys) push(`env.${k}`, fEnv[k] ?? null, tEnv[k] ?? null);
	return out;
}

export function specChanges(from: Deployment, to: Deployment): SpecChange[] {
	return specRows(from, to)
		.filter((row) => !row.same)
		.map(({ label, from: a, to: b }) => ({ label, from: a, to: b }));
}
