import { Code, ConnectError } from "@connectrpc/connect";
import { getErrorMessage } from "@/lib/error-handler";
import type { ReactNode } from "react";
import type { DescMethod } from "@bufbuild/protobuf";
import { queryMetrics } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { ErrorState } from "@/components/design/ErrorState";
import { Skeleton } from "@/components/design/Skeleton";
import { useHeldError } from "@/hooks/useHeldError";
import { MetricChart, type ChartMarker, type ChartSeries, type PinAction } from "./MetricChart";
const CHART_HEIGHT = 220;
export function MetricCard({
	title,
	series,
	loading,
	fetching,
	error,
	retry,
	from,
	to,
	unit,
	format,
	actions,
	markers,
	controls,
	filter,
	rpc = queryMetrics,
}: {
	title: string;
	series: ChartSeries[];
	loading: boolean;
	fetching: boolean;
	error: unknown;
	retry: () => Promise<unknown>;
	from: number;
	to: number;
	unit: string;
	format: (v: number) => string;
	actions: (t: number) => PinAction[];
	markers: ChartMarker[];
	controls?: ReactNode;
	filter?: ReactNode;
	rpc?: DescMethod;
}) {
	const heldError = useHeldError(error, fetching);
	const queryTooBroad = heldError instanceof ConnectError && heldError.code === Code.FailedPrecondition;
	const latestValue = (entry: ChartSeries) => {
		const point = entry.points.at(-1);
		return point === undefined ? "—" : format(point.v);
	};
	let content: ReactNode;
	if (queryTooBroad)
		content = (
			<p role="alert" className="flex items-center text-sm text-fg3" style={{ height: CHART_HEIGHT }}>
				{getErrorMessage(heldError)}
			</p>
		);
	else if (heldError !== null)
		content = <ErrorState error={heldError} what={title.toLowerCase()} rpc={rpc} onRetry={retry} />;
	else if (loading) content = <Skeleton style={{ height: CHART_HEIGHT }} />;
	else if (!series.some((entry) => entry.points.length > 0))
		content = (
			<div className="flex items-center justify-center text-[12px] text-fg3" style={{ height: CHART_HEIGHT }}>
				No data in this range
			</div>
		);
	else
		content = (
			<MetricChart
				title={title}
				series={series}
				from={from}
				to={to}
				fmt={format}
				highlight={null}
				unit={unit}
				height={CHART_HEIGHT}
				markers={markers}
				actions={actions}
			/>
		);
	return (
		<section aria-label={title} className="flex min-w-0 flex-col gap-2 rounded-card bg-panel px-5 pt-4 pb-3.5">
			<div className="flex flex-wrap items-center justify-between gap-2">
				<h3 className="m-0 text-[14px] font-medium">{title}</h3>
				{controls}
			</div>
			{series.length === 1 && (
				<div className="font-mono text-[23px] leading-tight tracking-tight tabular-nums">{latestValue(series[0]!)}</div>
			)}
			{filter}
			{content}
			<ul className="m-0 flex max-h-32 list-none flex-wrap gap-x-4 gap-y-1 overflow-y-auto p-0 text-[11px] text-fg3">
				{series.map((s) => (
					<li key={s.key} className="flex min-w-0 items-center gap-1.5">
						<svg width="14" height="4" className="shrink-0" aria-hidden="true">
							<line x1="0" x2="14" y1="2" y2="2" stroke={s.color} strokeWidth="2" strokeDasharray={s.dash} />
						</svg>
						<span className="break-all">{s.name}</span>
						<span className="shrink-0 font-mono text-fg2">{latestValue(s)}</span>
					</li>
				))}
			</ul>
		</section>
	);
}
