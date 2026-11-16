import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";
import { maybeTsMs } from "@/lib/time";

export interface InfraPoint {
	time: number;
	value?: number;
	out?: number;
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
