import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";

import { PartialError } from "@/components/design/ErrorState";
import { Skeleton } from "@/components/design/Skeleton";
import { useNow } from "@/hooks/useNow";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { getErrorMessage } from "@/lib/error-handler";
import { maybeTsMs } from "@/lib/time";
import { BYTES_PER_MI, formatMemory } from "@/lib/quantity";
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
		format: (value: number) => `${formatMemory(value / BYTES_PER_MI)}B`,
	},
];

function Signal({
	clusterId,
	nodeName,
	signal,
}: {
	clusterId: string;
	nodeName: string;
	signal: (typeof SIGNALS)[number];
}) {
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
			aggregation: "avg",
			attributes: { cluster: clusterId, node: nodeName },
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
	let content = (
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
	);
	if (result.isLoading) content = <Skeleton className="h-36 w-full" />;
	else if (result.error !== null)
		content = (
			<div className="min-h-36">
				<PartialError
					title={`${signal.title} unavailable`}
					detail={getErrorMessage(result.error, `Failed to load ${signal.title.toLowerCase()} usage`)}
					error={result.error}
					onRetry={async () => await result.refetch()}
				/>
			</div>
		);
	else if (empty)
		content = <p className="m-0 flex h-36 items-center justify-center text-center text-[12.5px] text-fg3">No data</p>;
	return (
		<div
			data-slot="node-runtime-signal"
			data-cluster-id={clusterId}
			data-node-name={nodeName}
			className="min-w-0 px-5 py-4"
		>
			<h3 className="m-0 mb-3 text-[13.5px] font-medium">{signal.title} usage</h3>
			{content}
		</div>
	);
}

export function NodeRuntimeSignals({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
	return (
		<div
			aria-label={`Usage for ${nodeName}`}
			className="grid grid-cols-1 divide-y divide-line border-t border-line sm:grid-cols-2 sm:divide-x sm:divide-y-0"
		>
			{SIGNALS.map((signal) => (
				<Signal
					key={`${clusterId}/${nodeName}/${signal.metric}`}
					clusterId={clusterId}
					nodeName={nodeName}
					signal={signal}
				/>
			))}
		</div>
	);
}
