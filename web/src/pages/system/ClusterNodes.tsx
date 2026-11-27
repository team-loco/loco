import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { getNodeAllocations, listClusterNodes } from "@gen/loco/cluster/v1/cluster-ClusterAdminService_connectquery";
import type { AllocationConsumer, GetNodeAllocationsResponse, NodeCapacity } from "@gen/loco/cluster/v1/cluster_pb";
import { keepPreviousData } from "@tanstack/react-query";
import { useShownPage } from "@/hooks/useTokenPage";
import { parseAsBoolean, parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { ErrorState, PartialError } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusBadge } from "@/components/design/StatusBadge";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { reservationQuantity as allocationQuantity } from "@/lib/reservation";
import { getErrorMessage } from "@/lib/error-handler";
import { pageParser, pageRangeLabel } from "@/lib/paging";
import { resourcePath } from "@/lib/routes";
import { seriesColor } from "@/pages/observability/context";
import { SpanTip, SpanTipTrigger } from "@/pages/observability/trace-viewer/SpanTip";

import { allocationRects, allocationTiles } from "./node-allocation";
import { Capacity } from "./Capacity";
import { NodeRuntimeSignals } from "./RuntimeSignals";
import { SystemTime } from "./shared";

const PAGE_SIZE = 10;
const PARAMS = {
	node: parseAsString.withDefault(""),
	workspace: parseAsString.withDefault(""),
	metric: parseAsStringLiteral(["cpu", "memory"]).withDefault("cpu"),
	nodeQ: parseAsString.withDefault(""),
	consumerQ: parseAsString.withDefault(""),
	nodesPage: pageParser.withDefault(0),
	consumersPage: pageParser.withDefault(0),
	others: parseAsBoolean.withDefault(false),
};

function percentage(requested: bigint, allocatable: bigint | undefined, known: boolean): string {
	if (!known || allocatable === undefined) return "—";
	if (allocatable === 0n) return requested === 0n ? "0%" : "Over capacity";
	return `${Math.round((Number(requested) / Number(allocatable)) * 100)}%`;
}

function nodeColumns(known: boolean): DataColumn<NodeCapacity>[] {
	return [
		{
			id: "name",
			header: "Node",
			size: 180,
			minSize: 110,
			meta: { primary: true, grow: true },
			cell: ({ row: { original } }) => <CellText text={original.name} className="font-mono text-[12px]" />,
		},
		{
			id: "cpu",
			header: "CPU",
			size: 75,
			minSize: 65,
			meta: { align: "end", cellClassName: "font-mono text-[12px]" },
			cell: ({ row: { original } }) =>
				percentage(
					original.cpuMillicoresRequested,
					original.cpuMillicoresAllocatable,
					known && !original.capacityStale,
				),
		},
		{
			id: "memory",
			header: "Memory",
			size: 80,
			minSize: 70,
			meta: { align: "end", cellClassName: "font-mono text-[12px]" },
			cell: ({ row: { original } }) =>
				percentage(original.memoryBytesRequested, original.memoryBytesAllocatable, known && !original.capacityStale),
		},
	];
}

function consumerColumns(workspace: string, choose: (id: string) => void): DataColumn<AllocationConsumer>[] {
	return [
		{
			id: "name",
			header: workspace === "" ? "Workspace" : "Resource",
			size: 220,
			minSize: 120,
			meta: { primary: true, grow: true },
			cell: ({ row: { original } }) => {
				if (original.kind === "resource" && original.resourceLinkAvailable)
					return (
						<CellText
							text={original.name}
							render={<Link to={resourcePath(original.orgId, original.workspaceId, original.id)} />}
							className="font-mono text-[12.5px]"
						/>
					);
				if (original.kind !== "workspace") return <CellText text={original.name} />;
				return (
					<Button
						variant="plain-link"
						onClick={() => {
							choose(original.id);
						}}
					>
						{original.name}
					</Button>
				);
			},
		},
		{
			id: "org",
			header: "Organization",
			size: 130,
			minSize: 90,
			meta: { hideOnMobile: true },
			cell: ({ row: { original } }) =>
				!original.orgLinkAvailable ? (
					<CellText text={original.orgName || "—"} />
				) : (
					<CellText
						text={original.orgName}
						render={<Link to={`/system/orgs/${original.orgId}`} />}
						className="text-fg3"
					/>
				),
		},
		{
			id: "resources",
			header: "Resources",
			size: 110,
			minSize: 100,
			meta: { hideOnMobile: true, align: "end" },
			cell: ({ row: { original } }) => original.resourceCount.toLocaleString(),
		},
		...([false, true] as const).map((memory): DataColumn<AllocationConsumer> => ({
			id: memory ? "memory" : "cpu",
			header: memory ? "Reserved memory" : "Reserved CPU",
			size: 145,
			minSize: 105,
			meta: { align: "end", cellClassName: "font-mono text-[12px]" },
			cell: ({ row: { original } }) =>
				allocationQuantity(memory ? original.memoryBytes : original.cpuMillicores, memory),
		})),
	];
}

function NodeReady({ node }: { node: NodeCapacity }) {
	if (node.ready === undefined) return <StatusBadge glyph="off" label="Unknown" />;
	if (!node.ready) return <StatusBadge glyph="warn" label="Not ready" />;
	return <StatusBadge glyph="ok" label="Ready" />;
}

function AllocationMap({
	data,
	choose,
	onOthers,
}: {
	data: GetNodeAllocationsResponse;
	choose: (id: string) => void;
	onOthers: () => void;
}) {
	const tiles = allocationTiles(data);
	const total = tiles.reduce((sum, tile) => sum + tile.value, 0);
	const rects = allocationRects(tiles);
	if (rects.length === 0)
		return <p className="m-0 flex h-[270px] items-center justify-center text-[13px] text-fg3">No reservations</p>;
	return (
		<div
			className="relative h-[270px] overflow-hidden rounded-[3px] bg-background"
			aria-label="Selected node reservation treemap"
			data-slot="allocation-treemap"
		>
			{rects.map((tile, index) => {
				const color = tile.available ? "var(--line2)" : seriesColor(index);
				const label = `${tile.name}: ${allocationQuantity(tile.value, data.memory)}, ${Math.round((tile.value / total) * 100)}%`;
				const style = {
					left: `calc(${tile.x}% + 1px)`,
					top: `calc(${tile.y}% + 1px)`,
					width: `calc(${tile.width}% - 2px)`,
					height: `calc(${tile.height}% - 2px)`,
					background: `color-mix(in oklab, ${color} 34%, var(--bg))`,
				};
				const className =
					"@container absolute block min-w-0 overflow-hidden rounded-[3px] text-left text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring hover:shadow-[inset_0_0_0_1px_var(--fg)]";
				const consumer = tile.consumer;
				let render = <div style={style} className={className} aria-label={label} />;
				if (consumer?.kind === "workspace")
					render = (
						<button
							type="button"
							style={style}
							className={className}
							aria-label={`Inspect ${label}`}
							onClick={() => {
								choose(consumer.id);
							}}
						/>
					);
				else if (consumer?.kind === "resource" && consumer.resourceLinkAvailable)
					render = (
						<Link
							style={style}
							className={className}
							aria-label={`Open ${label}`}
							to={resourcePath(consumer.orgId, consumer.workspaceId, consumer.id)}
						/>
					);
				else if (consumer?.kind === "other")
					render = (
						<button
							type="button"
							style={style}
							className={className}
							aria-label={`Inspect ${label}`}
							onClick={onOthers}
						/>
					);
				return (
					<SpanTipTrigger key={tile.id} entries={[{ title: tile.name, color, meta: label }]} render={render}>
						<span className="flex h-full flex-col justify-between overflow-hidden px-2 py-1.5">
							<span className="hidden truncate font-mono text-[11.5px] @[65px]:block">{tile.name}</span>
							{tile.height >= 22 && (
								<span className="hidden truncate font-mono text-[11px] text-fg2 @[65px]:block">
									{allocationQuantity(tile.value, data.memory)}
								</span>
							)}
						</span>
					</SpanTipTrigger>
				);
			})}
		</div>
	);
}

function AllocationPager({
	page,
	total,
	count,
	loading,
	placeholder,
	onPage,
}: {
	page: number;
	total: number;
	count: number;
	loading: boolean;
	placeholder: boolean;
	onPage: (page: number) => void;
}) {
	const label = count === 0 ? `0 of ${total}` : pageRangeLabel(page, PAGE_SIZE, total);
	const lastPage = Math.max(0, Math.ceil(total / PAGE_SIZE) - 1);
	const shown = useShownPage(page, label, placeholder);
	return (
		<Pager
			{...shown}
			total={total}
			pageSizes={[PAGE_SIZE]}
			loading={loading}
			onPrev={
				page === 0
					? null
					: () => {
							onPage(Math.min(page - 1, lastPage));
						}
			}
			onNext={
				(page + 1) * PAGE_SIZE >= total
					? null
					: () => {
							onPage(page + 1);
						}
			}
		/>
	);
}

export function ClusterNodes({ clusterId }: { clusterId: string }) {
	const [params, setParams] = useQueryStates(PARAMS, { history: "replace" });
	const nodes = useQuery(
		listClusterNodes,
		{
			clusterId,
			query: params.nodeQ,
			page: params.nodesPage,
			memory: params.metric === "memory",
		},
		{ refetchInterval: 30_000, placeholderData: keepPreviousData },
	);
	const firstNode = nodes.data?.nodes[0]?.name ?? "";
	const selected = params.node === "" ? firstNode : params.node;
	const sample = nodes.data?.sample;
	const inspector = useQuery(
		getNodeAllocations,
		{
			clusterId,
			node: selected,
			sampleId: sample?.id ?? "",
			workspaceId: params.workspace,
			memory: params.metric === "memory",
			query: params.consumerQ,
			page: params.consumersPage,
			remainderOnly: params.others,
		},
		{
			enabled: selected !== "" && sample !== undefined,
			placeholderData: (previous) => {
				const nodeMatches = previous?.node?.name === selected;
				const filterMatches =
					previous?.workspaceId === params.workspace && previous.memory === (params.metric === "memory");
				const scopeMatches = nodeMatches && filterMatches;
				const sampleMatches = previous?.sample?.id === sample?.id;
				return scopeMatches && sampleMatches ? previous : undefined;
			},
		},
	);
	const data = inspector.data;
	const node = data?.node;
	const choose = (id: string) => {
		void setParams({
			workspace: id,
			consumersPage: 0,
			consumerQ: "",
			others: false,
		});
	};
	const nodeTable = useDataTable("capacity-nodes", {
		data: nodes.data?.nodes ?? [],
		columns: nodeColumns(sample !== undefined && !sample.stale),
		getRowId: (item) => item.name,
	});
	const consumerTable = useDataTable("node-consumers", {
		data: data?.consumers ?? [],
		columns: consumerColumns(data?.workspaceId ?? params.workspace, choose),
		getRowId: (item) => item.id,
	});
	const changedSample = inspector.error instanceof ConnectError && inspector.error.code === Code.Aborted;
	const retryInspector = async () => {
		if (changedSample) await nodes.refetch();
		else await inspector.refetch();
	};
	return (
		<>
			<Section title="Capacity">
				{nodes.data === undefined && nodes.error === null && <Skeleton className="m-5 h-28" />}
				{nodes.data !== undefined && <Capacity summary={nodes.data} />}
			</Section>
			<Section
				title="Nodes"
				count={nodes.data?.totalCount.toLocaleString()}
				actions={
					<ToggleGroup
						variant="segmented"
						aria-label="Node allocation metric"
						value={[params.metric]}
						onValueChange={(values) => {
							const metric = values[0];
							if (metric === "cpu" || metric === "memory") void setParams({ metric, nodesPage: 0, consumersPage: 0 });
						}}
					>
						<ToggleGroupItem value="cpu">CPU</ToggleGroupItem>
						<ToggleGroupItem value="memory">Memory</ToggleGroupItem>
					</ToggleGroup>
				}
			>
				<SpanTip />
				{nodes.error !== null && (
					<div className="p-5">
						<ErrorState
							error={nodes.error}
							what="nodes"
							rpc={listClusterNodes}
							onRetry={async () => await nodes.refetch()}
						/>
					</div>
				)}
				<div className="grid min-w-0 grid-cols-1 xl:grid-cols-[minmax(300px,0.8fr)_minmax(0,1.2fr)]">
					<div className="min-w-0 border-b border-line xl:border-r xl:border-b-0">
						<div className="p-3">
							<SearchInput
								aria-label="Find a node"
								placeholder="Find a node"
								value={params.nodeQ}
								onChange={(event) => {
									void setParams({ nodeQ: event.target.value, nodesPage: 0 });
								}}
								onClear={() => {
									void setParams({ nodeQ: "", nodesPage: 0 });
								}}
							/>
						</div>
						<DataTable
							table={nodeTable}
							activeRowId={selected}
							empty={
								nodes.isSuccess ? (
									<p className="m-0 px-5 py-5 text-[13px] text-fg3">
										{params.nodeQ !== "" ? "No nodes match" : "No nodes reported"}
									</p>
								) : null
							}
							label="Nodes"
							band
							loading={nodes.isLoading}
							rowAction="open"
							rowLabel={({ original }) => `Inspect ${original.name}`}
							onRowClick={({ original }) => {
								void setParams({
									node: original.name,
									workspace: "",
									consumersPage: 0,
									consumerQ: "",
									others: false,
								});
							}}
						/>
						<AllocationPager
							page={params.nodesPage}
							total={nodes.data?.totalCount ?? 0}
							count={nodes.data?.nodes.length ?? 0}
							loading={nodes.isFetching}
							placeholder={nodes.isPlaceholderData}
							onPage={(nodesPage) => {
								void setParams({ nodesPage });
							}}
						/>
					</div>
					<div className="min-w-0 p-4" data-node={node?.name}>
						{sample === undefined && nodes.isSuccess && (
							<p className="m-0 text-[13px] text-fg3">Reservations not reported</p>
						)}
						{inspector.isLoading && <Skeleton className="h-96 w-full" />}
						{inspector.error !== null && (
							<PartialError
								title="Node reservations unavailable"
								detail={getErrorMessage(inspector.error, "Failed to load node reservations")}
								error={inspector.error}
								onRetry={retryInspector}
							/>
						)}
						{data !== undefined && node !== undefined && (
							<>
								<div className="mb-3 flex flex-wrap items-center gap-3">
									<h3 className="m-0 font-mono text-[13px] font-medium">{node.name}</h3>
									<NodeReady node={node} />
									<span className="ml-auto font-mono text-[11.5px] text-fg3">
										{node.resourceCount.toLocaleString()} resources
									</span>
								</div>
								<div className="mb-2 flex flex-wrap gap-x-5 gap-y-1 font-mono text-[12px]">
									<span>
										CPU {allocationQuantity(node.cpuMillicoresRequested, false)}
										<span className="text-fg3">
											{" "}
											/{" "}
											{node.cpuMillicoresAllocatable === undefined
												? "Not reported"
												: allocationQuantity(node.cpuMillicoresAllocatable, false)}
										</span>
									</span>
									<span>
										Memory {allocationQuantity(node.memoryBytesRequested, true)}
										<span className="text-fg3">
											{" "}
											/{" "}
											{node.memoryBytesAllocatable === undefined
												? "Not reported"
												: allocationQuantity(node.memoryBytesAllocatable, true)}
										</span>
									</span>
								</div>
								<div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-fg3">
									<span>{data.sample?.stale ? "Reservations stale" : "Reservations"}</span>
									<SystemTime ts={data.sample?.reportedAt} />
									<span className={node.capacityStale ? "text-warn-fg" : ""}>
										{node.capacityStale ? "Capacity stale" : "Capacity"}
									</span>
									<SystemTime ts={node.capacityReportedAt} />
								</div>
								<div className="mb-3 flex min-h-7 flex-wrap items-center gap-2 text-[12px]">
									<Button
										variant="plain-link"
										onClick={() => {
											choose("");
										}}
									>
										All workspaces
									</Button>
									{data.workspaceId !== "" && (
										<>
											<span className="text-fg3">/</span>
											<span className="font-mono">{data.workspaceName}</span>
										</>
									)}
								</div>
								<AllocationMap
									data={data}
									choose={choose}
									onOthers={() => {
										void setParams({
											others: true,
											consumersPage: 0,
											consumerQ: "",
										});
										document.getElementById("node-consumers")?.scrollIntoView({ block: "nearest" });
									}}
								/>
							</>
						)}
						{selected !== "" && (
							<div className="mt-4">
								<div className="mb-2 text-[12px] text-fg2">Usage · {selected} · 1h</div>
								<NodeRuntimeSignals clusterId={clusterId} nodeName={selected} />
							</div>
						)}
					</div>
				</div>
				{data !== undefined && node !== undefined && (
					<div className="border-t border-line" id="node-consumers" aria-busy={inspector.isFetching}>
						<div className="flex flex-wrap items-center justify-between gap-3 px-5 py-3">
							<h3 className="m-0 text-[13px] font-medium">
								{data.workspaceId === "" ? "Workspaces" : "Resources"} on {node.name}
								<span className="ml-2 font-mono text-[11.5px] font-normal text-fg3">
									{data.totalCount.toLocaleString()}
									{data.remainderOnly ? " in remainder" : ""}
								</span>
							</h3>
							<div className="flex items-center gap-2">
								{data.remainderOnly && (
									<Button
										variant="ghost"
										size="xs"
										onClick={() => {
											void setParams({ others: false, consumersPage: 0 });
										}}
									>
										Show all
									</Button>
								)}
								<SearchInput
									className="w-56 max-sm:w-full"
									aria-label="Search node consumers"
									placeholder="Find reservations"
									value={params.consumerQ}
									onChange={(event) => {
										void setParams({
											consumerQ: event.target.value,
											consumersPage: 0,
										});
									}}
									onClear={() => {
										void setParams({ consumerQ: "", consumersPage: 0 });
									}}
								/>
							</div>
						</div>
						<DataTable
							table={consumerTable}
							label="Node consumers"
							band
							empty={<p className="m-0 px-5 py-5 text-[13px] text-fg3">No reservations match</p>}
						/>
						<AllocationPager
							page={params.consumersPage}
							total={data.totalCount}
							count={data.consumers.length}
							loading={inspector.isFetching}
							placeholder={inspector.isPlaceholderData}
							onPage={(consumersPage) => {
								void setParams({ consumersPage });
							}}
						/>
					</div>
				)}
			</Section>
		</>
	);
}
