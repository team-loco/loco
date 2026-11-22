import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";
import { maybeTsMs } from "@/lib/time";

export interface InfraPoint {
	time: number;
	value?: number;
	out?: number;
}

/** The points of a resource's series, oldest first. */
export function valuePoints(series: MetricSeries[] | undefined, resourceId: string): InfraPoint[] {
	return networkPoints(series, undefined, resourceId);
}

/**
 * The limit usage was measured against: usage over limit utilization, summed over the buckets both
 * report. A constant limit divides out exactly.
 */
export function limitOf(usage: readonly InfraPoint[], utilization: readonly InfraPoint[]): number | undefined {
	const used = new Map(usage.map((point) => [point.time, point.value]));
	let total = 0;
	let share = 0;
	for (const point of utilization) {
		const value = used.get(point.time);
		if (value === undefined || point.value === undefined || point.value <= 0) continue;
		total += value;
		share += point.value;
	}
	return share > 0 ? total / share : undefined;
}

export function networkPoints(
	incoming: MetricSeries[] | undefined,
	outgoing: MetricSeries[] | undefined,
	resourceId: string,
): InfraPoint[] {
	const points = new Map<number, InfraPoint>();
	for (const [index, series] of [incoming, outgoing].entries()) {
		for (const stream of series ?? []) {
			if (stream.resourceId !== "" && stream.resourceId !== resourceId) continue;
			for (const point of stream.points) {
				const time = maybeTsMs(point.timestamp);
				if (time === undefined) continue;
				const row = points.get(time) ?? { time };
				row[index === 0 ? "value" : "out"] = point.value;
				points.set(time, row);
			}
		}
	}
	return [...points.values()].toSorted((a, b) => a.time - b.time);
}

export function sampleAt<T extends { time: number }>(points: readonly T[], time: number): T | undefined {
	return points.findLast((point) => point.time <= time);
}
