import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { MetricPointSchema, MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";

import { msToTimestamp } from "@/lib/time";

import { bandKey, bandRequest, latestBand, mergeBand, percentPoints, type BandTarget } from "./metric";

const WORKSPACE_ID = "0199d1c2-0000-7000-8000-000000000010";
const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const PRODUCTION_ID = "0199d1c2-0000-7000-8000-000000000002";
const STAGING_ID = "0199d1c2-0000-7000-8000-000000000003";
const REGION = "us-east-1";
const METRIC_NAME = "k8s.pod.cpu_request_utilization";
const NOW_MS = Date.UTC(2026, 9, 11, 12, 0, 0);
const HOUR_MS = 3_600_000;
const MINUTE_S = 60;

function target(overrides: Partial<BandTarget> = {}): BandTarget {
	return {
		workspaceId: WORKSPACE_ID,
		environmentId: PRODUCTION_ID,
		resourceId: RESOURCE_ID,
		region: REGION,
		range: "1h",
		metricName: METRIC_NAME,
		...overrides,
	};
}

describe("bandRequest", () => {
	test("asks for one aggregation of the resource in its environment and region", () => {
		expect(bandRequest(target(), "p50", NOW_MS)).toEqual({
			workspaceId: WORKSPACE_ID,
			environmentId: PRODUCTION_ID,
			region: REGION,
			resourceIds: [RESOURCE_ID],
			startTime: msToTimestamp(NOW_MS - HOUR_MS),
			endTime: msToTimestamp(NOW_MS),
			metricName: METRIC_NAME,
			intervalSeconds: MINUTE_S,
			aggregation: "p50",
		});
	});

	test("leaves the region empty to cover every region", () => {
		expect(bandRequest(target({ region: "" }), "max", NOW_MS).region).toBe("");
	});
});

describe("bandKey", () => {
	test("caches environments and regions separately", () => {
		expect(bandKey(target())).not.toEqual(bandKey(target({ environmentId: STAGING_ID })));
		expect(bandKey(target())).not.toEqual(bandKey(target({ region: "" })));
	});
});

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
		expect(latestBand(band)).toEqual({ min: undefined, median: 25, max: undefined });
	});
});
