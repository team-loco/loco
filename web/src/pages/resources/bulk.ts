import { formatCpu, formatMemory } from "@/lib/quantity";
import type { ReplicaRange } from "@/lib/sizing";

import type { ResourceRow } from "./table";

const LISTED_NAMES = 6;

export interface FanOutResult<T> {
	done: T[];
	failed: { item: T; error: unknown }[];
}

export async function fanOut<T>(items: readonly T[], run: (item: T) => Promise<unknown>): Promise<FanOutResult<T>> {
	const settled = await Promise.allSettled(items.map(run));
	const result: FanOutResult<T> = { done: [], failed: [] };
	items.forEach((item, i) => {
		const s = settled[i];
		if (s?.status === "fulfilled") result.done.push(item);
		else result.failed.push({ item, error: s?.reason });
	});
	return result;
}

export function fanOutSummary(verb: string, result: FanOutResult<unknown>): string {
	const total = result.done.length + result.failed.length;
	return `${verb} ${result.done.length.toString()} of ${total.toString()}`;
}

export interface ScaleForm {
	replicas: string;
	cpu: string;
	memory: string;
}

export interface ScaleChange {
	replicas?: number;
	cpu?: string;
	memory?: string;
}

const WHOLE_RE = /^\d+$/;

export function replicasError(replicas: string, range: ReplicaRange): string | undefined {
	const text = replicas.trim();
	if (text === "") return undefined;
	const n = Number(text);
	if (WHOLE_RE.test(text) && n >= range.min && n <= range.max) return undefined;
	return `Enter ${range.min.toString()} to ${range.max.toString()}`;
}

export function scaleChange(form: ScaleForm, range: ReplicaRange): ScaleChange | null {
	if (replicasError(form.replicas, range) !== undefined) return null;
	const change: ScaleChange = {};
	const replicas = form.replicas.trim();
	if (replicas !== "") change.replicas = Number(replicas);
	if (form.cpu !== "") change.cpu = form.cpu;
	if (form.memory !== "") change.memory = form.memory;
	return Object.keys(change).length === 0 ? null : change;
}

export interface CurrentSetup {
	replicas: string;
	cpu: string;
	memory: string;
}

const UNKNOWN = "—";

// Shows one value when every target agrees and the lowest to highest otherwise.
export function currentSetup(targets: readonly ResourceRow[]): CurrentSetup {
	const replicas = targets.flatMap((r) => (r.replicas === null ? [] : [r.replicas.min, r.replicas.max]));
	return {
		replicas: span(replicas, (n) => n.toString()),
		cpu: span(
			targets.map((r) => r.cpuMilli).filter((n) => n > 0),
			formatCpu,
		),
		memory: span(
			targets.map((r) => r.memMi).filter((n) => n > 0),
			formatMemory,
		),
	};
}

function span(values: number[], format: (n: number) => string): string {
	if (values.length === 0) return UNKNOWN;
	const low = Math.min(...values);
	const high = Math.max(...values);
	if (low === high) return format(low);
	const [lowNumber, unit] = format(low).split(" ");
	const top = format(high);
	return unit !== undefined && top.endsWith(` ${unit}`) ? `${lowNumber}–${top}` : `${format(low)}–${top}`;
}

export function scaleTargets(rows: readonly ResourceRow[], environmentId: string) {
	const targets = rows.filter((r) => r.cells.has(environmentId));
	return { targets, skipped: rows.length - targets.length };
}

export function scaleNote(targets: number, skipped: number, environment: string): string {
	if (targets === 0) return `None of these are deployed to ${environment}.`;
	if (skipped === 0) return `Applies to every region of ${environment}.`;
	return `${targets.toString()} run in ${environment}, in every region. ${skipped.toString()} not deployed there will be skipped.`;
}

export function deleteText(rows: readonly ResourceRow[]): string {
	const running = rows.filter((r) => r.cells.size > 0).length;
	const removal = "Deployments, domains and variables are removed in every environment. This can't be undone.";
	if (running === 0) return removal;
	if (rows.length === 1) return `It is running. ${removal}`;
	return `${running.toString()} ${running === 1 ? "is" : "are"} running. ${removal}`;
}

export function listedNames(names: readonly string[]): { shown: string[]; more: number } {
	return { shown: names.slice(0, LISTED_NAMES), more: Math.max(0, names.length - LISTED_NAMES) };
}
