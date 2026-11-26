import { useTransport } from "@connectrpc/connect-query";
import { queryMetrics } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";

import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useNow } from "@/hooks/useNow";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { systemObservabilityPath } from "@/lib/routes";
import { maybeTsMs } from "@/lib/time";
import { formatBytes } from "@/lib/traffic";
import { seriesColor } from "@/pages/observability/context";
import { MetricChart } from "@/pages/observability/MetricChart";

const HOUR_MS = 3_600_000;
const SIGNALS = [
	{
		title: "CPU",
		metric: "k8s.node.cpu.usage",
		scale: 1,
		unit: " cores",
		format: (value: number) => `${value.toFixed(2)} cores`,
	},
	{
		title: "Memory",
		metric: "k8s.node.memory.working_set",
		scale: 1024 ** 3,
		unit: " GiB",
		format: formatBytes,
	},
];

function Signal({ clusterId, signal }: { clusterId: string; signal: (typeof SIGNALS)[number] }) {
	const transport = useTransport();
	const now = useNow(60_000).getTime();
	const result = useQuery(
		metricsQueryOptions(transport, {
			workspaceId: "",
			installation: true,
			resourceIds: [],
			environmentId: "",
			spanMs: HOUR_MS,
			metricName: signal.metric,
			aggregation: "max",
			attributes: { cluster: clusterId },
		}),
	);
	const series = (result.data ?? []).map((item, index) => ({
		key: String(index),
		name: signal.title,
		color: seriesColor(index),
		dashed: false,
		limits: [],
		points: item.points.flatMap((point) => {
			const t = maybeTsMs(point.timestamp);
			return t === undefined ? [] : [{ t, v: point.value / signal.scale }];
		}),
	}));
	const empty = series.every((item) => item.points.length === 0);
	return (
		<div data-slot="cluster-runtime-signal" data-cluster-id={clusterId} className="min-w-0 px-5 py-4">
			<h3 className="m-0 mb-3 text-[13.5px] font-medium">{signal.title}</h3>
			{result.isLoading ? (
				<Skeleton className="h-36 w-full" />
			) : result.error !== null ? (
				<ErrorState
					error={result.error}
					what={signal.title}
					rpc={queryMetrics}
					onRetry={async () => await result.refetch()}
				/>
			) : empty ? (
				<p className="m-0 flex h-36 items-center justify-center text-center text-[12.5px] text-fg3">No data</p>
			) : (
				<MetricChart
					title={signal.title}
					series={series}
					from={now - HOUR_MS}
					to={now}
					fmt={(value) => signal.format(value * signal.scale)}
					unit={signal.unit}
					highlight={null}
					markers={[]}
					height={145}
					actions={() => []}
				/>
			)}
		</div>
	);
}

export function RuntimeSignals({ clusterId }: { clusterId: string }) {
	return (
		<Section
			title="Metrics"
			meta="Max node · 1h"
			actions={
				<Link
					className="text-[12.5px] text-fg2 underline underline-offset-4"
					to={systemObservabilityPath("metrics", {
						q: `@cluster:${clusterId}`,
					})}
				>
					Metrics
				</Link>
			}
		>
			<div className="grid grid-cols-1 divide-y divide-line lg:grid-cols-2 lg:divide-x lg:divide-y-0">
				{SIGNALS.map((signal) => (
					<Signal key={`${clusterId}/${signal.metric}`} clusterId={clusterId} signal={signal} />
				))}
			</div>
		</Section>
	);
}
