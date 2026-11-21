import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { MetricPointSchema, MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";

import { msToTimestamp } from "@/lib/time";

import { mergeBand, percentPoints, scaleBand, sharedLimit, tightestPoint, usageSummary } from "./metric";

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
