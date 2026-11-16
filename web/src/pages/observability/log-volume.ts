import type { LogBucket } from "@gen/loco/observability/v1/observability_pb";
import { tsMs } from "@/lib/time";
import { levelOf, type Level } from "./query";

export function logVolume(groups: LogBucket[], from: number, to: number, widthMs: number) {
	const first = Math.floor(from / widthMs) * widthMs;
	const count = Math.floor(to / widthMs) - Math.floor(from / widthMs) + 1;
	const points = Array.from({ length: count }, (_, i) => ({
		start: first + i * widthMs,
		counts: new Map<Level, number>(),
	}));
	const totals = new Map<Level, number>();
	for (const group of groups) {
		const index = Math.floor((tsMs(group.startTime) - first) / widthMs);
		const point = points[index];
		if (point === undefined) continue;
		const level = levelOf(group.severity);
		const n = Number(group.count);
		point.counts.set(level, (point.counts.get(level) ?? 0) + n);
		totals.set(level, (totals.get(level) ?? 0) + n);
	}
	return { points, totals };
}
