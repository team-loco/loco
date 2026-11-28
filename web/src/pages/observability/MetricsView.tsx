import { MetricCard } from "./MetricCard";
import { useState, type ReactNode } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueries, useQuery } from "@tanstack/react-query";
import { parseAsStringLiteral, useQueryStates } from "nuqs";
import { getResource, listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import {
	queryDeploymentBuckets,
	getDeployment,
} from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import {
	queryMetrics,
	queryRequestMetrics,
} from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { RocketIcon, ScrollTextIcon } from "lucide-react";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import { Button } from "@/components/design/Button";
import { Dialog, DialogContent, DialogTitle } from "@/components/design/Dialog";
import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { SegmentedChoice } from "@/components/design/SegmentedChoice";
import { Skeleton } from "@/components/design/Skeleton";
import { Input } from "@/components/design/Input";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { useNow } from "@/hooks/useNow";
import { useCurrentDeployments } from "@/hooks/useWorkspaceDeployments";
import { aroundWindow, resolveWindow, stepSeconds, windowSpan } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";
import { formatCpu, formatMemory, BYTES_PER_MI, MILLI_PER_CORE } from "@/lib/quantity";
import { formatPercent } from "@/lib/compute-chart";
import { TRAFFIC_METRICS, trafficValue } from "@/lib/traffic";
import { isWorker } from "@/lib/resource-regions";
import { getErrorMessage } from "@/lib/error-handler";
import { EmptyWorkspace } from "@/pages/dashboard/EmptyWorkspace";
import { SpecDialogContent } from "@/pages/resource/SpecDialog";
import { useObs } from "./context";
import { ViewHeader } from "./ViewHeader";
import { type ChartMarker, type ChartSeries, type PinAction } from "./MetricChart";
import { MetricActivity } from "./MetricActivity";
import { MetricResources, MetricRegions } from "./MetricSelectors";
import { errorPercentSeries, pathLatencySeries, regionalSeries, REGION_LABEL, regionalColors } from "./metric-series";
import { quoteValue, unquoteValue, type Token } from "./query";

const REFRESH_MS = 60_000;
const PREFS = {
	units: parseAsStringLiteral(["Usage", "% of limit"]).withDefault("Usage"),
	replicas: parseAsStringLiteral(["Total", "Per replica"]).withDefault("Total"),
	comparison: parseAsStringLiteral(["Compare", "Combine"]).withDefault("Compare"),
	percentile: parseAsStringLiteral(["p50", "p95", "p99"]).withDefault("p95"),
	traffic: parseAsStringLiteral(["Requests", "5xx"]).withDefault("Requests"),
	latency: parseAsStringLiteral(["Combined", "By path"]).withDefault("Combined"),
};
function isMetricToken(token: Token): boolean {
	if (token.neg) return false;
	return token.key === "resource" || token.key === "region";
}
function Choice({
	label,
	value,
	choices,
	onChange,
}: {
	label: string;
	value: string;
	choices: string[];
	onChange: (value: string) => void;
}) {
	return (
		<SegmentedChoice
			label={label}
			value={value}
			options={choices.map((choice) => ({ value: choice, label: choice }))}
			onChange={onChange}
			className="flex w-auto grid-cols-none flex-wrap"
		/>
	);
}
function DeploymentDetails({ id, onClose }: { id: string; onClose: () => void }) {
	const transport = useTransport();
	const query = useQuery(createQueryOptions(getDeployment, { deploymentId: id }, { transport }));
	const deployment = query.data?.deployment;
	let content: ReactNode = (
		<DialogContent>
			<DialogTitle>Deployment</DialogTitle>
			<Skeleton className="h-32" />
		</DialogContent>
	);
	if (query.error !== null)
		content = (
			<DialogContent>
				<DialogTitle>Deployment</DialogTitle>
				<ErrorState error={query.error} what="deployment" rpc={getDeployment} onRetry={query.refetch} />
			</DialogContent>
		);
	if (deployment !== undefined)
		content = (
			<SpecDialogContent
				resourceName={deployment.resourceName}
				deployments={[deployment]}
				deploymentId={id}
				compareId={null}
				onChange={() => {}}
			/>
		);
	return (
		<Dialog
			open
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			{content}
		</Dialog>
	);
}
export function MetricsView() {
	const { workspaceId, environmentId, environmentLoading, resourcesLoading, tokens, text, setQuery, range, goTo } =
		useObs();
	const transport = useTransport();
	const [prefs, setPrefs] = useQueryStates(PREFS);
	const [path, setPath] = useState("");
	const [pathInput, setPathInput] = useState("");
	const [deploymentId, setDeploymentId] = useState<string | null>(null);
	const now = useNow(REFRESH_MS);
	const { from, to } = resolveWindow(range, now.getTime());
	const spanMs = windowSpan(range);
	const endMs = range.kind === "absolute" ? range.to : undefined;
	const slugs = tokens.filter((t) => t.key === "resource" && !t.neg).map((t) => unquoteValue(t.value));
	const regions = tokens.filter((t) => t.key === "region" && !t.neg).map((t) => unquoteValue(t.value));
	const selectedQueries = useQueries({
		queries: slugs.map((slug) => ({
			...createQueryOptions(getResource, { key: { case: "slugKey", value: { workspaceId, slug } } }, { transport }),
			enabled: !resourcesLoading,
			refetchInterval: REFRESH_MS,
		})),
	});
	const selected = selectedQueries.flatMap((q) => (q.data?.resource === undefined ? [] : [q.data.resource]));
	const ready = !environmentLoading && !resourcesLoading;
	const resolving = selectedQueries.some((q) => q.isPending);
	const selectedError = selectedQueries.find((q) => q.error !== null)?.error ?? null;
	const supported = tokens.every(isMetricToken) && text === "";
	const selectionReady = !resolving && selectedError === null;
	const scopeReady = ready && supported;
	const resourceIds = selected.map((r) => r.id);
	const single = selected.length === 1;
	const statusRegions = selected[0]?.environmentStatuses
		.find((status) => status.environmentId === environmentId)
		?.regions.filter((status) => regions.length === 0 || regions.includes(status.region));
	const readyReplicas = statusRegions?.reduce((total, status) => total + status.readyReplicas, 0);
	const desiredReplicas = statusRegions?.reduce((total, status) => total + status.desiredReplicas, 0);
	const current = useCurrentDeployments(single ? selected : [], environmentId)[0] ?? [];
	const worker = single && isWorker(selected[0]!, current);
	const pathGrouping = single && prefs.latency === "By path";
	const pathMode = !worker && pathGrouping;
	const visibility = [true, true, !worker, !worker && prefs.traffic === "5xx", !worker && !pathMode, single, single];
	const perPod = single && prefs.replicas === "Per replica";
	const combineResources = slugs.length === 0 || prefs.comparison === "Combine";
	const percent = prefs.units === "% of limit";
	const regionQuery = useQuery({
		...createQueryOptions(
			listWorkspaceResources,
			{ workspaceId, environmentId, resourceIds, pageSize: 1, includeFacets: true },
			{ transport },
		),
		enabled: ready && selectionReady,
	});
	const hasResources = (regionQuery.data?.resources.length ?? 0) > 0;
	const selectionEnabled = scopeReady && selectionReady;
	const enabled = selectionEnabled && hasResources;
	const selectionLoading = !ready || resolving;
	const pageLoading = selectionLoading || regionQuery.isPending;
	const common = {
		workspaceId,
		environmentId,
		resourceIds,
		spanMs,
		endMs,
		regions,
		byRegion: true,
		readableResources: true,
		combineResources,
		enabled,
	};
	const definitions = [
		{
			metricName: percent ? "k8s.pod.cpu_limit_utilization" : "k8s.pod.cpu.usage",
			aggregation: percent ? "max" : "avg",
			sumPods: !percent && !perPod,
			byPod: perPod,
		},
		{
			metricName: percent ? "k8s.pod.memory.working_set_limit_utilization" : "k8s.pod.memory.working_set",
			aggregation: percent ? "max" : "avg",
			sumPods: !percent && !perPod,
			byPod: perPod,
		},
		TRAFFIC_METRICS[0],
		TRAFFIC_METRICS[1],
		{ ...TRAFFIC_METRICS[3], aggregation: prefs.percentile },
		{
			metricName: "k8s.pod.network.io",
			aggregation: "rate",
			attributes: { direction: "receive" },
		},
		{
			metricName: "k8s.pod.network.io",
			aggregation: "rate",
			attributes: { direction: "transmit" },
		},
	];
	const queries = useQueries({
		queries: definitions.map((def, index) =>
			metricsQueryOptions(transport, {
				...common,
				...def,
				enabled: enabled && (visibility[index] ?? false),
				combineResources: perPod && index < 2 ? false : combineResources,
			}),
		),
	});

	const pathQuery = useQuery({
		...createQueryOptions(
			queryRequestMetrics,
			{
				workspaceId,
				environmentId,
				resourceId: selected[0]?.id ?? "",
				startTime: msToTimestamp(from),
				endTime: msToTimestamp(to),
				intervalSeconds: stepSeconds(spanMs),
				regions,
				path,
			},
			{ transport },
		),
		enabled: enabled && pathMode,
		refetchInterval: endMs === undefined ? REFRESH_MS : false,
	});
	const regionNames = [
		...new Set([
			...(regionQuery.data?.facets?.regions.map((r) => r.region) ?? []),
			...queries.flatMap((q) => q.data?.map((s) => s.labels[REGION_LABEL] ?? "") ?? []),
			...(pathQuery.data?.buckets.map((b) => b.region) ?? []),
			...regions,
		]),
	]
		.filter(Boolean)
		.toSorted();
	const colors = regionalColors(regionNames);
	const names = new Map(selected.filter(() => selected.length > 1).map((r) => [r.id, r.name]));
	const series = queries.map((q, i) => {
		let divisor = 1;
		if (i < 2 && percent) divisor = 0.01;
		else if (i === 1) divisor = BYTES_PER_MI;
		return regionalSeries(q.data ?? [], names, colors, divisor);
	});
	const deploymentQuery = useQuery({
		...createQueryOptions(
			queryDeploymentBuckets,
			{
				workspaceId,
				environmentId,
				resourceIds,
				startTime: msToTimestamp(from),
				endTime: msToTimestamp(to),
				bucketCount: 200,
			},
			{ transport },
		),
		enabled: enabled && resourceIds.length <= 100,
		refetchInterval: endMs === undefined ? REFRESH_MS : false,
	});
	const buckets = deploymentQuery.data?.buckets ?? [];
	const markers: ChartMarker[] = buckets.flatMap((b) =>
		b.meanCreatedAt === undefined
			? []
			: [
					{
						t: Number(b.meanCreatedAt.seconds) * 1000,
						label: `${b.deploymentCount.toString()} deployment${b.deploymentCount === 1 ? "" : "s"}`,
						title: "Deployment",
						color: "var(--fg3)",
					},
				],
	);
	const actions = (t: number): PinAction[] => {
		const near = buckets.find((b) => Math.abs(Number(b.meanCreatedAt?.seconds ?? 0) * 1000 - t) < (to - from) * 0.02);
		const result: PinAction[] = [
			{
				key: "logs",
				icon: <ScrollTextIcon />,
				label: "View logs",
				run: () => {
					goTo("logs", { range: aroundWindow(t, now.getTime()), focusTs: t });
				},
			},
		];
		const deployment = near?.onlyDeployment;
		if (deployment !== undefined)
			result.push({
				key: "deployment",
				icon: <RocketIcon />,
				label: "View deployment",
				run: () => {
					setDeploymentId(deployment.id);
				},
			});
		return result;
	};
	const changeFilter = (key: "resource" | "region", values: string[]) => {
		setQuery(
			[
				...tokens.filter((t) => t.key !== key),
				...values.map((value) => ({ key, value: quoteValue(value), neg: false }) satisfies Token),
			],
			text,
		);
	};
	const selectResources = (resources: Resource[]) => {
		changeFilter(
			"resource",
			resources.map((r) => r.slug),
		);
	};
	const renderCard = (
		index: number,
		title: string,
		unit: string,
		format: (v: number) => string,
		controls?: ReactNode,
		override?: ChartSeries[],
		dependencies: number[] = [],
	) => {
		const results = [queries[index]!, ...dependencies.map((dependency) => queries[dependency]!)];
		return (
			<MetricCard
				title={title}
				series={override ?? series[index] ?? []}
				loading={pageLoading || results.some((query) => query.isLoading)}
				fetching={results.some((query) => query.isFetching)}
				error={results.find((query) => query.error !== null)?.error ?? null}
				retry={async () => await Promise.all(results.map(async (q) => await q.refetch()))}
				from={from}
				to={to}
				unit={unit}
				format={format}
				markers={markers}
				actions={actions}
				controls={controls}
			/>
		);
	};
	const latencySeries = pathMode
		? pathLatencySeries(pathQuery.data?.buckets ?? [], prefs.percentile, colors)
		: (series[4] ?? []);
	const latencyQuery = pathMode ? pathQuery : queries[4]!;
	let selectionError: ReactNode;
	if (selectedError !== null)
		selectionError = (
			<p role="alert" className="text-sm text-bad-fg">
				{getErrorMessage(selectedError, "Could not load selected resources")}
			</p>
		);
	if (!supported)
		selectionError = (
			<p role="alert" className="text-sm text-fg3">
				These filters are not supported for metrics.{" "}
				<Button
					variant="link"
					onClick={() => {
						setQuery([], "");
					}}
				>
					Clear filters
				</Button>
			</p>
		);
	if (regionQuery.error !== null) {
		return (
			<>
				<ViewHeader />
				<ErrorState
					error={regionQuery.error}
					what="resources"
					rpc={listWorkspaceResources}
					onRetry={regionQuery.refetch}
				/>
			</>
		);
	}
	if (regionQuery.data?.resources.length === 0) {
		return (
			<>
				<ViewHeader />
				<EmptyWorkspace wholeWorkspace />
			</>
		);
	}

	return (
		<>
			<ViewHeader />
			<div className="flex flex-wrap items-center gap-2">
				<MetricResources
					workspaceId={workspaceId}
					environmentId={environmentId}
					selected={selected}
					ready={ready}
					pending={resolving}
					onChange={selectResources}
				/>
				<MetricRegions
					regions={regionNames}
					selected={regions}
					colors={colors}
					onChange={(values) => {
						changeFilter("region", values);
					}}
				/>
				{selected.length > 1 && (
					<Choice
						label="Resource comparison"
						value={prefs.comparison}
						choices={["Combine", "Compare"]}
						onChange={(comparison) =>
							void setPrefs({
								comparison: comparison as typeof prefs.comparison,
							})
						}
					/>
				)}
			</div>
			{single && (
				<div className="flex flex-wrap items-center gap-x-4 gap-y-2">
					<span className="text-[18px] tracking-tight">{selected[0]!.name}</span>
					{readyReplicas !== undefined && (
						<span className="text-[12px] text-fg3">
							{readyReplicas} / {desiredReplicas} replicas ready
						</span>
					)}
					<Button
						variant="link"
						className="ml-auto"
						onClick={() => {
							goTo("logs");
						}}
					>
						View logs
					</Button>
				</div>
			)}
			{selectionError}
			{!worker && (
				<Section title="Traffic" card={false}>
					<div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
						{renderCard(
							2,
							prefs.traffic === "Requests" ? "Requests" : "Server errors",
							prefs.traffic === "Requests" ? "req/s" : "%",
							(v) => trafficValue(v, prefs.traffic === "Requests" ? "req/s" : "%"),
							<Choice
								label="Traffic metric"
								value={prefs.traffic}
								choices={["Requests", "5xx"]}
								onChange={(traffic) => void setPrefs({ traffic: traffic as typeof prefs.traffic })}
							/>,
							prefs.traffic === "5xx" ? errorPercentSeries(series[2] ?? [], series[3] ?? []) : undefined,
							prefs.traffic === "5xx" ? [3] : [],
						)}
						<MetricCard
							title="Response time"
							rpc={pathMode ? queryRequestMetrics : queryMetrics}
							filter={
								pathMode && (
									<form
										className="flex gap-2"
										onSubmit={(event) => {
											event.preventDefault();
											setPath(pathInput);
										}}
									>
										<Input
											aria-label="Filter paths"
											placeholder="Filter paths…"
											value={pathInput}
											onChange={(event) => {
												setPathInput(event.target.value);
											}}
											maxLength={256}
										/>
										<Button type="submit" variant="outline">
											Apply
										</Button>
									</form>
								)
							}
							series={latencySeries}
							loading={pageLoading || latencyQuery.isLoading}
							fetching={latencyQuery.isFetching}
							error={latencyQuery.error}
							retry={latencyQuery.refetch}
							from={from}
							to={to}
							unit="ms"
							format={(v) => trafficValue(v, "ms")}
							markers={markers}
							actions={actions}
							controls={
								<div className="flex flex-wrap gap-2">
									<Choice
										label="Latency percentile"
										value={prefs.percentile}
										choices={["p50", "p95", "p99"]}
										onChange={(percentile) =>
											void setPrefs({
												percentile: percentile as typeof prefs.percentile,
											})
										}
									/>
									{single && (
										<Choice
											label="Group latency by"
											value={prefs.latency}
											choices={["Combined", "By path"]}
											onChange={(latency) =>
												void setPrefs({
													latency: latency as typeof prefs.latency,
												})
											}
										/>
									)}
								</div>
							}
						/>
					</div>
				</Section>
			)}
			<Section
				title="Compute"
				card={false}
				actions={
					<div className="flex flex-wrap gap-2">
						{percent && <span className="self-center text-[11px] text-fg3">Busiest replica</span>}
						<Choice
							label="Compute units"
							value={prefs.units}
							choices={["Usage", "% of limit"]}
							onChange={(units) => void setPrefs({ units: units as typeof prefs.units })}
						/>
						{single && (
							<Choice
								label="Replica view"
								value={prefs.replicas}
								choices={["Total", "Per replica"]}
								onChange={(replicas) => void setPrefs({ replicas: replicas as typeof prefs.replicas })}
							/>
						)}
					</div>
				}
			>
				<div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
					{renderCard(0, "CPU", percent ? "%" : "vCPU", percent ? formatPercent : (v) => formatCpu(v * MILLI_PER_CORE))}
					{renderCard(1, "Memory", percent ? "%" : "MiB", percent ? formatPercent : formatMemory)}
				</div>
			</Section>
			{single && (
				<Section title="Network & health" card={false}>
					<div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
						{renderCard(
							5,
							"Network",
							"B/s",
							(v) => trafficValue(v, "B/s"),
							undefined,
							[
								...(series[5] ?? []).map((s) => ({
									...s,
									key: `receive-${s.key}`,
									name: `Received · ${s.name}`,
								})),
								...(series[6] ?? []).map((s) => ({
									...s,
									key: `transmit-${s.key}`,
									name: `Sent · ${s.name}`,
									dash: "7 3",
								})),
							],
							[6],
						)}
						<MetricActivity resourceId={selected[0]!.id} regions={regions} from={from} to={to} />
					</div>
				</Section>
			)}
			{deploymentId !== null && (
				<DeploymentDetails
					id={deploymentId}
					onClose={() => {
						setDeploymentId(null);
					}}
				/>
			)}
		</>
	);
}
