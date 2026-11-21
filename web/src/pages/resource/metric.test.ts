import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { MetricPointSchema, MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";

import { msToTimestamp } from "@/lib/time";

import {
	chartWindow,
	mergeBand,
	percentPoints,
	replicaChart,
	scaleBand,
	sharedLimit,
	tightestPoint,
	usageSummary,
} from "./metric";

const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const STAGING_ID = "0199d1c2-0000-7000-8000-000000000003";
const NOW_MS = Date.UTC(2026, 9, 11, 12, 0, 0);
const HOUR_MS = 3_600_000;

describe("percentPoints", () => {
	test("keeps the resource's points as percentages in time order", () => {
		const later = create(MetricPointSchema, { timestamp: msToTimestamp(NOW_MS), value: 0.5 });
		const earlier = create(MetricPointSchema, { timestamp: msToTimestamp(NOW_MS - HOUR_MS), value: 0.25 });
		const series = [
			create(MetricSeriesSchema, { resourceId: RESOURCE_ID, points: [later, earlier] }),
			create(MetricSeriesSchema, { resourceId: STAGING_ID, points: [later] }),
		];
		expect(percentPoints(series, RESOURCE_ID)).toEqual([
			{ time: NOW_MS - HOUR_MS, pct: 25 },
			{ time: NOW_MS, pct: 50 },
		]);
	});
});

describe("mergeBand", () => {
	test("joins min, median and max by time", () => {
		const band = mergeBand({
			min: [{ time: 1, pct: 10 }],
			p50: [
				{ time: 1, pct: 20 },
				{ time: 2, pct: 25 },
			],
			max: [{ time: 1, pct: 40 }],
		});
		expect(band).toEqual([
			{ time: 1, median: 20, band: [10, 40] },
			{ time: 2, median: 25, band: null },
		]);
		expect(usageSummary(band)).toEqual({ average: 22.5, highest: 40 });
		expect(scaleBand(band, [{ from: -Infinity, limit: 500 }])).toEqual([
			{ time: 1, median: 100, band: [50, 200], limit: 500 },
			{ time: 2, median: 125, band: null, limit: 500 },
		]);
	});
});

test("uses absolute units only when every charted region has the same size", () => {
	expect(sharedLimit([500, 500])).toBe(500);
	expect(sharedLimit([500, 1000])).toBeUndefined();
	expect(sharedLimit([500, 0])).toBeUndefined();
	expect(sharedLimit([])).toBeUndefined();
});

describe("scaleBand across a resize", () => {
	const steps = [
		{ from: 0, limit: 100 },
		{ from: HOUR_MS, limit: 200 },
	];
	const band = scaleBand(
		[
			{ time: HOUR_MS - 1, median: 50, band: [40, 90] },
			{ time: HOUR_MS, median: 50, band: [40, 60] },
		],
		steps,
	);

	test("keeps each point in the units of the limit it ran under", () => {
		expect(band.map((p) => [p.median, p.limit])).toEqual([
			[50, 100],
			[100, 200],
		]);
	});

	test("finds the peak closest to its own limit", () => {
		expect(tightestPoint(band)?.time).toBe(HOUR_MS - 1);
	});
});

describe("replicaChart", () => {
	const pod = (name: string, ...values: number[]) =>
		create(MetricSeriesSchema, {
			resourceId: RESOURCE_ID,
			labels: { "k8s.pod.name": name },
			points: values.map((value, i) =>
				create(MetricPointSchema, { timestamp: msToTimestamp(NOW_MS + i * 60_000), value }),
			),
		});

	test("draws each replica in the limit's units and names the busiest", () => {
		const chart = replicaChart([pod("web-a1", 0.2, 0.4), pod("web-b2", 0.5, 0.9)], RESOURCE_ID, [
			{ from: 0, limit: 500 },
		]);
		expect(chart?.replicas).toEqual(["web-a1", "web-b2"]);
		expect(chart?.rows[1]).toEqual({ time: NOW_MS + 60_000, limit: 500, "web-a1": 200, "web-b2": 450 });
		expect(chart?.highest).toEqual({ replica: "web-b2", value: 450, share: 0.9 });
	});

	test("leaves charts with too many replicas to the band", () => {
		const pods = Array.from({ length: 13 }, (_, i) => pod(`web-${i.toString()}`, 0.1));
		expect(replicaChart(pods, RESOURCE_ID, undefined)).toBeUndefined();
	});
});

describe("chartWindow", () => {
	test("spans the per-replica query when its lines are drawn, even if the band failed", () => {
		expect(chartWindow(true, NOW_MS, { from: 0, to: 1 }, "1h")).toEqual({ from: NOW_MS - HOUR_MS, to: NOW_MS });
	});
	test("keeps the band's span without per-replica lines", () => {
		expect(chartWindow(false, NOW_MS, { from: 5, to: 9 }, "1h")).toEqual({ from: 5, to: 9 });
	});
});
