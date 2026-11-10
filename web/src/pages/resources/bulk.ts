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

export function replicasError(form: ScaleForm): string | undefined {
	const text = form.replicas.trim();
	if (text === "") return undefined;
	return /^\d+$/.test(text) ? undefined : "Enter a whole number";
}

export function scaleChange(form: ScaleForm): ScaleChange | null {
	if (replicasError(form) !== undefined) return null;
	const change: ScaleChange = {};
	const replicas = form.replicas.trim();
	const cpu = form.cpu.trim();
	const memory = form.memory.trim();
	if (replicas !== "") change.replicas = Number(replicas);
	if (cpu !== "") change.cpu = cpu;
	if (memory !== "") change.memory = memory;
	return Object.keys(change).length === 0 ? null : change;
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
