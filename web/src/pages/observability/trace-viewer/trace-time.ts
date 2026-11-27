import { AROUND_MS } from "@/lib/obs-time";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampNanos } from "../traces";
import type { TraceSummary } from "../traces";

export function traceBounds(trace: Pick<TraceSummary, "start" | "duration">) {
	const start = Number(trace.start / 1_000_000n);
	return { start, end: start + Number(trace.duration / 1_000_000n) };
}

export function logOffset(trace: Pick<TraceSummary, "start">, timestamp: Timestamp | undefined): bigint {
	return timestampNanos(timestamp) - trace.start;
}

export function traceLogWindow(trace: Pick<TraceSummary, "start" | "duration">, now: number) {
	const { start, end } = traceBounds(trace);
	return { kind: "absolute" as const, from: start - AROUND_MS, to: Math.min(now, end + AROUND_MS) };
}
