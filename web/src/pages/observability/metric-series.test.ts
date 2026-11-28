import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { MetricSeriesSchema, RequestMetricBucketSchema } from "@gen/loco/observability/v1/observability_pb";
import { regionalColors, regionalSeries, errorPercentSeries, pathLatencySeries } from "./metric-series";

const regions = ["us-ca-1", "us-va-1", "eu-de-1", "ap-sg-1"];
test("regions keep distinct colors when the selection or discovery order changes", () => {
	const all = regionalColors(regions);
	expect(new Set(all.values()).size).toBe(4);
	expect(regionalColors([regions[1]!]).get(regions[1]!)).toBe(all.get(regions[1]!));
	expect(regionalColors(regions.toReversed())).toEqual(all);
});
test("error percentages pair resource, region and timestamp without mixing their traffic", () => {
	const point = (value: number) => ({ timestamp: { seconds: 60n }, value });
	const raw = [
		create(MetricSeriesSchema, { resourceId: "r", labels: { "loco.region": "a" }, points: [point(100)] }),
		create(MetricSeriesSchema, { resourceId: "r", labels: { "loco.region": "b" }, points: [point(10)] }),
	];
	const colors = regionalColors(["a", "b"]);
	const requests = regionalSeries(raw, new Map(), colors);
	const errors = regionalSeries(
		[create(MetricSeriesSchema, { resourceId: "r", labels: { "loco.region": "b" }, points: [point(2)] })],
		new Map(),
		colors,
	);
	expect(errorPercentSeries(requests, errors).map((s) => s.points[0]?.v)).toEqual([0, 20]);
});
test("literal Unicode paths remain separate across regions and percentiles", () => {
	const buckets = [
		create(RequestMetricBucketSchema, {
			region: "a",
			path: "/café/123",
			timestamp: { seconds: 60n },
			p50Millis: 10,
			p95Millis: 50,
		}),
		create(RequestMetricBucketSchema, {
			region: "b",
			path: "/café/123",
			timestamp: { seconds: 60n },
			p50Millis: 20,
			p95Millis: 90,
		}),
	];
	const series = pathLatencySeries(buckets, "p95", regionalColors(["a", "b"]));
	expect(series.map((s) => s.points[0]?.v)).toEqual([50, 90]);
	expect(new Set(series.map((s) => s.key)).size).toBe(2);
	expect(series[0]?.name).toBe("/café/123 · a");
});
