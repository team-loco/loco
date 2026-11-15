import type { TraceSummary } from "../traces";

export function traceBounds(trace: Pick<TraceSummary, "start" | "duration">) {
	const start = Number(trace.start / 1_000_000n);
	return { start, end: start + Number(trace.duration / 1_000_000n) };
}
