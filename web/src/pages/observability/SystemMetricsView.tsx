import { useTimePreferences } from "@/lib/use-time-preferences";
import { useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { queryMetrics } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { ScrollTextIcon, WaypointsIcon } from "lucide-react";

import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useNow } from "@/hooks/useNow";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { isUnimplemented } from "@/lib/error-handler";
import { aroundWindow, resolveWindow, windowSpan } from "@/lib/obs-time";
import { maybeTsMs, formatHourMinute } from "@/lib/time";

import { seriesColor, useObs } from "./context";
import { MetricChart, type ChartSeries, type PinAction } from "./MetricChart";
import { ObsHero } from "./ObsEmpty";
import type { FieldKey } from "./query";
import { QueryBar } from "./QueryBar";
import { slices, SYSTEM_GROUPS, visibleGroups, type SystemChart } from "./system-metrics";
import { ViewHeader } from "./ViewHeader";

const NOW_TICK_MS = 60_000;
const CHART_HEIGHT = 190;

function valueCounts(key: FieldKey): [string, number][] {
	return key === "component" ? SYSTEM_GROUPS.flatMap((g) => (g.component === null ? [] : [[g.component, 0]])) : [];
}

/** The charts of the metrics Loco's own components export, grouped by component. */
export function SystemMetricsView() {
	const timePref = useTimePreferences();
	const transport = useTransport();
	const { range, tokens, goTo } = useObs();
	const now = useNow(NOW_TICK_MS);
	const { from, to } = resolveWindow(range, now.getTime());
	const spanMs = windowSpan(range);
	const endMs = range.kind === "absolute" ? range.to : undefined;
	const groups = visibleGroups(tokens);
	const sliced = slices(tokens);
	const charts = groups.flatMap((group) =>
		group.charts.map((chart) => ({ group, chart, key: `${group.key}/${chart.key}` })),
	);
	const results = useQueries({
		queries: charts.flatMap(({ group, chart }) =>
			sliced.map((slice) =>
				metricsQueryOptions(transport, {
					workspaceId: "",
					installation: true,
					environmentId: "",
					resourceIds: [],
					spanMs,
					endMs,
					metricName: chart.metric,
					aggregation: chart.aggregation,
					region: slice.region,
					attributes: group.component === null ? slice.attributes : { component: group.component, ...slice.attributes },
				}),
			),
		),
	});
	const resultsOf = (index: number) => results.slice(index * sliced.length, (index + 1) * sliced.length);
	const seriesOf = (index: number): ChartSeries[] =>
		sliced.flatMap((slice, i) =>
			(resultsOf(index)[i]?.data ?? []).map((series, n) => ({
				key: `${slice.name}/${String(n)}`,
				name: slice.name,
				color: seriesColor(i),
				dashed: false,
				limits: [],
				points: series.points.flatMap((p) => {
					const t = maybeTsMs(p.timestamp);
					return t === undefined ? [] : [{ t, v: p.value }];
				}),
			})),
		);
	const actions = (t: number): PinAction[] => {
		const around = aroundWindow(t, Date.now());
		return [
			{
				key: "logs",
				icon: <ScrollTextIcon />,
				label: `Logs around ${formatHourMinute(t, timePref)}`,
				sub: "±5 minutes, same filters",
				run: () => {
					goTo("logs", { range: around, focusTs: t });
				},
			},
			{
				key: "traces",
				icon: <WaypointsIcon />,
				label: `Traces around ${formatHourMinute(t, timePref)}`,
				sub: "±5 minutes, same filters",
				run: () => {
					goTo("traces", { range: around });
				},
			},
		];
	};

	if (results.length > 0 && results.every((r) => r.error !== null && isUnimplemented(r.error))) {
		return (
			<>
				<ViewHeader />
				<ObsHero
					view="metrics"
					title="Metrics aren't available here yet"
					body="This installation has no metrics store connected. Loco's own metrics show up here once it does."
				/>
			</>
		);
	}

	const card = (chart: SystemChart, index: number) => {
		const series = seriesOf(index);
		const chartResults = resultsOf(index);
		const failed = chartResults.find((r) => r.error !== null && !r.isFetching)?.error ?? null;
		const loading = failed === null && chartResults.some((r) => r.isLoading);
		return (
			<section
				key={charts[index]?.key}
				aria-label={chart.title}
				className="flex min-w-0 flex-col gap-3.5 rounded-card bg-panel px-5 pt-[18px] pb-4"
			>
				<div className="flex items-baseline gap-2">
					<h3 className="m-0 text-[15px] font-medium tracking-[-0.01em]">{chart.title}</h3>
					<span className="font-mono text-[12px] text-fg3">{chart.noun}</span>
				</div>
				{failed !== null ? (
					<ErrorState
						error={failed}
						what={chart.title}
						rpc={queryMetrics}
						onRetry={async () => await Promise.all(chartResults.map(async (r) => await r.refetch()))}
					/>
				) : loading ? (
					<Skeleton className="w-full" style={{ height: CHART_HEIGHT }} />
				) : series.every((s) => s.points.length === 0) ? (
					<div
						className="flex items-center justify-center rounded-lg border border-dashed border-line2 text-sm text-fg3"
						style={{ height: CHART_HEIGHT }}
					>
						No data in this range
					</div>
				) : (
					<MetricChart
						title={chart.title}
						series={series}
						from={from}
						to={to}
						fmt={chart.format}
						unit={chart.unit}
						highlight={null}
						markers={[]}
						height={CHART_HEIGHT}
						actions={actions}
					/>
				)}
			</section>
		);
	};

	return (
		<>
			<ViewHeader />
			<QueryBar valueCounts={valueCounts} />
			{groups.length === 0 && <p className="m-0 text-fg3">No component matches these filters.</p>}
			{groups.map((group) => (
				<Section key={group.key} title={group.title} card={false}>
					<div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
						{charts.flatMap((c, index) => (c.group === group ? [card(c.chart, index)] : []))}
					</div>
				</Section>
			))}
		</>
	);
}
