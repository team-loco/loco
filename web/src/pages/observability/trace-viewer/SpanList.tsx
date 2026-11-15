import { useState } from "react";
import { useTable } from "@tanstack/react-table";
import {
	DataTable,
	DataTablePager,
	dataTableFeatures,
	sortOptions,
	type DataColumn,
} from "@/components/design/DataTable";
import { SearchInput } from "@/components/design/SearchInput";
import { cn } from "@/lib/utils";
import { selfTime } from "../flame";
import { useObs } from "../context";
import { durationLabel, type TraceSummary } from "../traces";
import { traceServiceLabel, traceServiceColor } from "../trace-service";

interface Operation {
	key: string;
	name: string;
	resource: string;
	service: string;
	count: number;
	average: number;
	execution: number;
	share: number;
}
export function operations(trace: TraceSummary, label: (span: TraceSummary["root"]) => string): Operation[] {
	const groups = new Map<string, Operation>();
	for (const span of trace.spans) {
		const key = JSON.stringify([span.resourceId || span.serviceName, span.name]);
		const group = groups.get(key) ?? {
			key,
			name: span.name,
			resource: label(span),
			service: span.serviceName,
			count: 0,
			average: 0,
			execution: 0,
			share: 0,
		};
		group.count++;
		group.average += Number(span.durationNanos);
		group.execution += Number(selfTime(trace, span));
		groups.set(key, group);
	}
	const total = [...groups.values()].reduce((sum, g) => sum + g.execution, 0);
	return [...groups.values()].map((g) => ({
		...g,
		average: g.average / g.count,
		share: total === 0 ? 0 : g.execution / total,
	}));
}
const nanos = (value: number) => durationLabel(BigInt(Math.round(value)));
const COLUMNS: DataColumn<Operation>[] = [
	{
		id: "name",
		header: "Operation",
		accessorKey: "name",
		size: 260,
		cell: ({ row }) => <span className="font-mono">{row.original.name}</span>,
	},
	{
		id: "resource",
		header: "Resource",
		accessorKey: "resource",
		size: 140,
		cell: ({ row }) => (
			<span className="flex items-center gap-1.5">
				<i className="size-2 shrink-0 rounded-full" style={{ background: traceServiceColor(row.original.service) }} />
				{row.original.resource}
			</span>
		),
	},
	{
		id: "count",
		header: "Spans",
		accessorKey: "count",
		size: 80,
		cell: ({ row }) => (
			<span className={cn("font-mono", row.original.count > 5 && "rounded-sm bg-warn-bg px-1 text-warn-fg")}>
				{row.original.count}
			</span>
		),
	},
	{
		id: "average",
		header: "Avg duration",
		accessorKey: "average",
		size: 120,
		cell: ({ row }) => <span className="font-mono">{nanos(row.original.average)}</span>,
	},
	{
		id: "execution",
		header: "Exec time",
		accessorKey: "execution",
		size: 120,
		cell: ({ row }) => <span className="font-mono">{nanos(row.original.execution)}</span>,
	},
	{
		id: "share",
		header: "% of exec time",
		accessorKey: "share",
		size: 160,
		cell: ({ row }) => (
			<span className="flex items-center gap-2">
				<span className="h-1.5 flex-1 overflow-hidden rounded-sm bg-panel2">
					<i
						className="block h-full"
						style={{
							width: `${String(row.original.share * 100)}%`,
							background: traceServiceColor(row.original.service),
						}}
					/>
				</span>
				<span className="font-mono text-fg3">{Math.round(row.original.share * 100)}%</span>
			</span>
		),
	},
];
const PAGE_SIZES = [25, 50, 100];
export function SpanList({ trace }: { trace: TraceSummary }) {
	const { resourceById } = useObs();
	const [search, setSearch] = useState("");
	const data = operations(trace, (span) => traceServiceLabel(span, resourceById.get(span.resourceId)?.name)).filter(
		(g) => `${g.name} ${g.resource}`.toLowerCase().includes(search.toLowerCase()),
	);
	const table = useTable({
		...sortOptions,
		features: dataTableFeatures,
		columns: COLUMNS,
		data,
		getRowId: (row) => row.key,
		initialState: { sorting: [{ id: "execution", desc: true }], pagination: { pageIndex: 0, pageSize: 25 } },
	});
	return (
		<section aria-label="Span list" className="tv-table min-w-0 overflow-hidden rounded-md border border-line">
			<div className="border-b border-line p-2">
				<SearchInput
					value={search}
					placeholder="Filter operations"
					aria-label="Filter operations"
					onChange={(e) => {
						setSearch(e.target.value);
						table.setPageIndex(0);
					}}
					onClear={() => {
						setSearch("");
						table.setPageIndex(0);
					}}
				/>
			</div>
			<DataTable table={table} empty={<p className="p-5 text-fg3">No operations match this filter.</p>} />
			<DataTablePager table={table} pageSizes={PAGE_SIZES} />
		</section>
	);
}
