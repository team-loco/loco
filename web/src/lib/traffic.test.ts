import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "bun:test";
import { MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "./time";
import { errorShare, latestTraffic, trafficPoints } from "./traffic";

describe("gateway traffic", () => {
	it("joins metrics by resource and bucket without converting rates again", () => {
		const series = (resourceId: string, value: number) =>
			create(MetricSeriesSchema, {
				resourceId,
				points: [{ timestamp: msToTimestamp(60_000), value }],
			});
		const points = trafficPoints([
			[series("a", 10), series("b", 30)],
			[series("a", 2), series("b", 1)],
			[series("a", 1000)],
			[series("a", 190)],
		]);
		expect(latestTraffic(points, "a")).toEqual({
			resourceId: "a",
			time: 60_000,
			requests: 10,
			errors: 2,
			bytes: 1000,
			latency: 190,
		});
		expect(errorShare(latestTraffic(points))).toBe(7.5);
	});
	it("keeps missing samples distinct from recorded zero and avoids summing stale buckets", () => {
		expect(latestTraffic([])).toBeUndefined();
		expect(errorShare(undefined)).toBeUndefined();
		expect(errorShare({ resourceId: "a", time: 0, requests: 0 })).toBe(0);
		expect(errorShare({ resourceId: "a", time: 0, requests: 10 })).toBeUndefined();
		expect(
			latestTraffic([
				{ resourceId: "a", time: 1, requests: 100 },
				{ resourceId: "b", time: 2, requests: 2 },
			])?.requests,
		).toBe(2);
	});
});
