import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "@/lib/time";
import { limitOf, networkPoints, sampleAt } from "./infra-series";

const series = (points: [number, number][], resourceId = "resource") => [
	create(MetricSeriesSchema, {
		resourceId,
		points: points.map(([time, value]) => ({ timestamp: msToTimestamp(time), value })),
	}),
];

describe("replica metric samples", () => {
	test("joins network directions without inventing missing rates", () => {
		expect(
			networkPoints(
				series([
					[2000, 30],
					[1000, 10],
				]),
				series([
					[1000, 0],
					[3000, 20],
				]),
				"resource",
			),
		).toEqual([
			{ time: 1000, value: 10, out: 0 },
			{ time: 2000, value: 30 },
			{ time: 3000, out: 20 },
		]);
		expect(networkPoints(series([[1000, 99]], "other"), undefined, "resource")).toEqual([]);
		expect(networkPoints(undefined, undefined, "resource")).toEqual([]);
	});
	test("uses the latest observed sample at the trace start or cursor", () => {
		const points = [
			{ time: 1000, value: 0 },
			{ time: 2000, value: 50 },
		];
		expect(sampleAt(points, 999)).toBeUndefined();
		expect(sampleAt(points, 1000)?.value).toBe(0);
		expect(sampleAt(points, 1500)?.value).toBe(0);
		expect(sampleAt(points, 2000)?.value).toBe(50);
		expect(sampleAt(points, 3000)?.value).toBe(50);
		const empty: typeof points = [];
		expect(sampleAt(empty, 1000)).toBeUndefined();
	});
	test("recovers the limit usage was measured against", () => {
		const usage = [
			{ time: 1000, value: 0.25 },
			{ time: 2000, value: 0.5 },
			{ time: 3000, value: 0.4 },
		];
		const utilization = [
			{ time: 1000, value: 0.5 },
			{ time: 2000, value: 1 },
			{ time: 4000, value: 0.9 },
		];
		expect(limitOf(usage, utilization)).toBe(0.5);
		expect(limitOf(usage, [{ time: 1000, value: 0 }])).toBeUndefined();
		expect(limitOf([], utilization)).toBeUndefined();
	});
});
