import type { MetricSeries, RequestMetricBucket } from "@gen/loco/observability/v1/observability_pb";
import type { ChartSeries } from "./MetricChart";

export const REGION_LABEL = "loco.region";
// Derive hue from identity, so filtering or discovering a region never changes another region's color.
export function regionalColors(regions: string[]): Map<string, string> {
	return new Map(
		regions.map((region) => {
			let hash = 2166136261;
			for (const letter of region) hash = Math.imul(hash ^ letter.charCodeAt(0), 16777619) >>> 0;
			return [region, `oklch(from var(--series-0) l c ${String((hash % 360) + hash / 2 ** 32)})`];
		}),
	);
}
const STYLES = ["", "7 3", "2 3", "9 3 2 3"];
function seriesKey(parts: string[]): string {
	return btoa(encodeURIComponent(JSON.stringify(parts)))
		.replaceAll("+", "-")
		.replaceAll("/", "_")
		.replaceAll("=", "");
}
export function metricSeriesKey(series: MetricSeries): string {
	return seriesKey([series.resourceId, series.labels[REGION_LABEL] ?? "", series.labels["k8s.pod.name"] ?? ""]);
}
export function regionalSeries(
	data: MetricSeries[],
	names: Map<string, string>,
	colors: Map<string, string>,
	divisor = 1,
): ChartSeries[] {
	const identities = [...new Set(data.map((s) => `${s.resourceId}/${s.labels["k8s.pod.name"] ?? ""}`))].toSorted();
	return data
		.map((series) => {
			const region = series.labels[REGION_LABEL] ?? "";
			const pod = series.labels["k8s.pod.name"] ?? "";
			const identity = `${series.resourceId}/${pod}`;
			const name = [names.get(series.resourceId), pod, region || "Unknown region"].filter(Boolean).join(" · ");
			return {
				key: metricSeriesKey(series),
				name,
				color: colors.get(region) ?? "var(--fg3)",
				dashed: false,
				dash: STYLES[identities.indexOf(identity) % STYLES.length] ?? "",
				limits: [],
				points: series.points.flatMap((point) =>
					point.timestamp === undefined
						? []
						: [
								{
									t: Number(point.timestamp.seconds) * 1000,
									v: point.value / divisor,
								},
							],
				),
			};
		})
		.toSorted((a, b) => a.name.localeCompare(b.name) || a.key.localeCompare(b.key));
}
export function errorPercentSeries(requests: ChartSeries[], errors: ChartSeries[]): ChartSeries[] {
	const byKey = new Map(errors.map((s) => [s.key, new Map(s.points.map((p) => [p.t, p.v]))]));
	return requests.map((series) => ({
		...series,
		points: series.points.flatMap((p) =>
			p.v <= 0 ? [] : [{ t: p.t, v: ((byKey.get(series.key)?.get(p.t) ?? 0) / p.v) * 100 }],
		),
	}));
}
export function pathLatencySeries(
	buckets: RequestMetricBucket[],
	percentile: "p50" | "p95" | "p99",
	colors: Map<string, string>,
): ChartSeries[] {
	const paths = [...new Set(buckets.map((b) => b.path))].toSorted();
	const groups = Map.groupBy(buckets, (b) => seriesKey([b.region, b.path]));
	return [...groups]
		.map(([key, rows]) => {
			const row = rows[0]!;
			return {
				key,
				name: `${row.path} · ${row.region || "Unknown region"}`,
				color: colors.get(row.region) ?? "var(--fg3)",
				dashed: false,
				dash: STYLES[paths.indexOf(row.path) % STYLES.length] ?? "",
				limits: [],
				points: rows.flatMap((b) =>
					b.timestamp === undefined
						? []
						: [
								{
									t: Number(b.timestamp.seconds) * 1000,
									v: b[`${percentile}Millis`],
								},
							],
				),
			};
		})
		.toSorted((a, b) => a.name.localeCompare(b.name) || a.key.localeCompare(b.key));
}
