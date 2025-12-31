import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	Tooltip,
	TooltipContent,
	TooltipProvider,
	TooltipTrigger,
} from "@/components/ui/tooltip";
import { useStreamLogs } from "@/hooks/useStreamLogs";
import {
	Search,
	RefreshCw,
	Download,
	Pause,
	Play,
	ArrowUpDown,
	Copy,
	Check,
} from "lucide-react";
import { toast } from "sonner";
import { useState, useRef, useEffect } from "react";
import type { LogEntry } from "@/gen/resource/v1/resource_pb";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import {
	flexRender,
	getCoreRowModel,
	getPaginationRowModel,
	getSortedRowModel,
	getFilteredRowModel,
	useReactTable,
	type ColumnDef,
	type SortingState,
} from "@tanstack/react-table";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";

interface LogsViewerProps {
	appId: string;
	isLoading?: boolean;
}

export function LogsViewer({ appId, isLoading = false }: LogsViewerProps) {
	const [tailLimit] = useState<number | undefined>(100);
	const {
		logs,
		isLoading: logsLoading,
		refetch,
	} = useStreamLogs(appId, tailLimit);
	const [searchTerm, setSearchTerm] = useState("");
	const [isFollowing, setIsFollowing] = useState(true);
	const [sorting, setSorting] = useState<SortingState>([]);
	const [columnSizing, setColumnSizing] = useState({});
	const [pagination, setPagination] = useState({
		pageIndex: 0,
		pageSize: 20,
	});
	const [copiedIndex, setCopiedIndex] = useState<number | null>(null);
	const [timezoneMode, setTimezoneMode] = useState<"relative" | "utc">(
		"relative"
	);
	const logsEndRef = useRef<HTMLDivElement>(null);

	const handleCopyLog = (logText: string, index: number) => {
		navigator.clipboard.writeText(logText);
		setCopiedIndex(index);
		toast.success("Log copied to clipboard");
		setTimeout(() => setCopiedIndex(null), 2000);
	};

	const handleRefresh = () => {
		refetch();
		toast.success("Refreshing logs...");
	};

	const paddingClass = "px-2 py-1";

	useEffect(() => {
		if (isFollowing && logs.length > 0) {
			logsEndRef.current?.scrollIntoView({ behavior: "smooth" });
		}
	}, [logs, isFollowing]);

	const getTimezoneAbbr = (): string => {
		const date = new Date();
		const tzName =
			Intl.DateTimeFormat("en-US", { timeZoneName: "short" })
				.formatToParts(date)
				.find((part) => part.type === "timeZoneName")?.value || "UTC";
		return tzName;
	};

	const formatTimestamp = (ts?: {
		seconds?: bigint;
		nanos?: number;
	}): string => {
		if (!ts?.seconds) return "";
		const secondsNum =
			typeof ts.seconds === "bigint" ? Number(ts.seconds) : ts.seconds;
		const date = new Date(secondsNum * 1000);

		if (timezoneMode === "utc") {
			return date.toLocaleString("en-US", {
				month: "short",
				day: "2-digit",
				year: "numeric",
				hour: "2-digit",
				minute: "2-digit",
				second: "2-digit",
				hour12: false,
				timeZone: "UTC",
			});
		}

		return date.toLocaleString("en-US", {
			month: "short",
			day: "2-digit",
			year: "numeric",
			hour: "2-digit",
			minute: "2-digit",
			second: "2-digit",
			hour12: false,
		});
	};

	const handleExport = () => {
		const logsText = logs
			.map(
				(log) =>
					`[${formatTimestamp(log.timestamp)}] ${log.level || "INFO"} [${
						log.podName
					}] ${log.log}`
			)
			.join("\n");
		const element = document.createElement("a");
		element.setAttribute(
			"href",
			"data:text/plain;charset=utf-8," + encodeURIComponent(logsText)
		);
		element.setAttribute("download", `logs-${appId}-${Date.now()}.txt`);
		element.style.display = "none";
		document.body.appendChild(element);
		element.click();
		document.body.removeChild(element);
	};

	const columns: ColumnDef<LogEntry>[] = [
		{
			accessorKey: "timestamp",
			header: ({ column }) => (
				<button
					onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
					className="flex items-center gap-1 hover:bg-slate-200/30 dark:hover:bg-slate-800/30 px-1 py-0.5 rounded transition-colors"
				>
					<span>
						TIME{" "}
						<span className="text-xs opacity-70">
							({timezoneMode === "utc" ? "UTC" : getTimezoneAbbr()})
						</span>
					</span>
					<ArrowUpDown className="h-2 w-2" />
				</button>
			),
			cell: ({ row }) => (
				<span className="text-slate-600 dark:text-slate-400 shrink-0 text-xs">
					{formatTimestamp(row.original.timestamp)}
				</span>
			),
			size: 60,
		},
		{
			accessorKey: "log",
			header: "MESSAGE",
			cell: ({ row }) => (
				<span className="text-slate-700 dark:text-slate-300 overflow-hidden text-ellipsis line-clamp-1 text-xs">
					{row.original.log}
				</span>
			),
		},
		{
			id: "copy",
			header: () => null,
			cell: ({ row }) => (
				<div className="flex justify-end">
					<Tooltip>
						<TooltipTrigger asChild>
							<button
								onClick={(e) => {
									e.stopPropagation();
									handleCopyLog(row.original.log, row.index);
								}}
								className="p-1 hover:bg-accent/50 rounded transition-colors opacity-60 hover:opacity-100"
							>
								{copiedIndex === row.index ? (
									<Check className="h-3 w-3 text-green-500" />
								) : (
									<Copy className="h-3 w-3" />
								)}
							</button>
						</TooltipTrigger>
						<TooltipContent>Copy log</TooltipContent>
					</Tooltip>
				</div>
			),
			size: 40,
		},
	];

	const table = useReactTable({
		data: logs,
		columns,
		state: {
			sorting,
			pagination,
			globalFilter: searchTerm,
			columnSizing,
		},
		onSortingChange: setSorting,
		onPaginationChange: setPagination,
		onColumnSizingChange: setColumnSizing,
		getCoreRowModel: getCoreRowModel(),
		getSortedRowModel: getSortedRowModel(),
		getPaginationRowModel: getPaginationRowModel(),
		getFilteredRowModel: getFilteredRowModel(),
		columnResizeMode: "onChange",
		globalFilterFn: (row, _columnId, filterValue: string) => {
			return row.original.log.toLowerCase().includes(filterValue.toLowerCase());
		},
	});

	if (isLoading) {
		return (
			<Card className="animate-pulse">
				<CardContent className="p-6">
					<div className="h-6 bg-main/20 rounded w-1/4"></div>
				</CardContent>
			</Card>
		);
	}

	return (
		<Card className="border-2">
			<CardHeader>
				<CardTitle>Logs</CardTitle>
			</CardHeader>
			<CardContent className="space-y-4">
				{/* Toolbar */}
				<TooltipProvider>
					<div className="flex flex-col sm:flex-row gap-2 items-start sm:items-center">
						<div className="relative flex-1 w-full">
							<Search className="absolute left-3 top-2.5 h-4 w-4 text-foreground opacity-50" />
							<Input
								placeholder="Search logs..."
								value={searchTerm}
								onChange={(e) => setSearchTerm(e.target.value)}
								className="pl-9"
							/>
						</div>

						<div className="flex gap-2 w-full sm:w-auto">
							<Tooltip>
								<TooltipTrigger asChild>
									<Button
										variant={timezoneMode === "utc" ? "default" : "outline"}
										size="sm"
										onClick={() =>
											setTimezoneMode(
												timezoneMode === "utc" ? "relative" : "utc"
											)
										}
										className="flex-1 sm:flex-none"
									>
										{timezoneMode === "utc" ? "UTC" : "EST"}
									</Button>
								</TooltipTrigger>
								<TooltipContent>
									{timezoneMode === "utc"
										? "Show relative time"
										: "Show UTC time"}
								</TooltipContent>
							</Tooltip>
							<Tooltip>
								<TooltipTrigger asChild>
									<Button
										variant="outline"
										size="sm"
										onClick={() => setIsFollowing(!isFollowing)}
										className="flex-1 sm:flex-none"
									>
										{isFollowing ? (
											<Pause className="w-4 h-4" />
										) : (
											<Play className="w-4 h-4" />
										)}
									</Button>
								</TooltipTrigger>
								<TooltipContent>
									{isFollowing ? "Stop following" : "Follow logs"}
								</TooltipContent>
							</Tooltip>
							<Tooltip>
								<TooltipTrigger asChild>
									<Button
										variant="outline"
										size="sm"
										onClick={handleRefresh}
										disabled={logsLoading}
										className="flex-1 sm:flex-none"
									>
										<RefreshCw
											className={`w-4 h-4 ${logsLoading ? "animate-spin" : ""}`}
										/>
									</Button>
								</TooltipTrigger>
								<TooltipContent>Refresh logs</TooltipContent>
							</Tooltip>
							<Tooltip>
								<TooltipTrigger asChild>
									<Button
										variant="outline"
										size="sm"
										onClick={handleExport}
										className="flex-1 sm:flex-none"
									>
										<Download className="w-4 h-4" />
									</Button>
								</TooltipTrigger>
								<TooltipContent>Export logs</TooltipContent>
							</Tooltip>
						</div>
					</div>
				</TooltipProvider>

				{/* Logs Table */}
				<div className="overflow-hidden rounded-md border border-slate-200/50 dark:border-slate-800/50">
					<div className="bg-white dark:bg-slate-950 font-mono text-[8px]">
						<Table className="w-full table-fixed">
							<TableHeader className="bg-slate-50 dark:bg-slate-900/50 border-b border-slate-200/30 dark:border-slate-800/30">
								{table.getHeaderGroups().map((headerGroup) => (
									<TableRow
										key={headerGroup.id}
										className="hover:bg-transparent"
									>
										{headerGroup.headers.map((header) => (
											<TableHead
												key={header.id}
												style={{
													width: `${header.getSize()}px`,
												}}
												className="text-slate-500 dark:text-slate-500 font-normal px-2 py-1.5 text-xs relative group"
											>
												<div className="flex items-center justify-between">
													<div>
														{header.isPlaceholder
															? null
															: flexRender(
																	header.column.columnDef.header,
																	header.getContext()
															  )}
													</div>
													<div
														onMouseDown={header.getResizeHandler()}
														onTouchStart={header.getResizeHandler()}
														className="select-none touch-none h-5 w-1 bg-transparent group-hover:bg-blue-500 cursor-col-resize opacity-0 group-hover:opacity-100 transition-opacity"
													/>
												</div>
											</TableHead>
										))}
									</TableRow>
								))}
							</TableHeader>
						</Table>
					</div>
					<div className="max-h-96 overflow-y-auto bg-white dark:bg-slate-950">
						<Table className="w-full table-fixed">
							<TableBody>
								{table.getRowModel().rows?.length ? (
									table.getRowModel().rows.map((row) => (
										<TableRow
											key={row.id}
											className="hover:bg-slate-50 dark:hover:bg-slate-900/30 border-b border-slate-100/50 dark:border-slate-700/50 last:border-0 transition-colors"
										>
											{row.getVisibleCells().map((cell) => (
												<TableCell
													key={cell.id}
													style={{
														width: `${cell.column.getSize()}px`,
													}}
													className={paddingClass}
												>
													{flexRender(
														cell.column.columnDef.cell,
														cell.getContext()
													)}
												</TableCell>
											))}
										</TableRow>
									))
								) : (
									<TableRow>
										<TableCell
											colSpan={columns.length}
											className="h-24 text-center text-slate-500 dark:text-slate-400"
										>
											{logs.length === 0
												? "No logs yet"
												: `No logs match "${searchTerm}"`}
										</TableCell>
									</TableRow>
								)}
								<TableRow>
									<TableCell colSpan={columns.length}>
										<div ref={logsEndRef} />
									</TableCell>
								</TableRow>
							</TableBody>
						</Table>
					</div>
				</div>

				{/* Pagination */}
				<div className="flex items-center justify-between text-sm">
					<div className="text-foreground opacity-70">
						Showing {table.getRowModel().rows.length} of{" "}
						{table.getFilteredRowModel().rows.length} logs
					</div>
					<div className="flex items-center gap-2">
						<div className="hidden sm:flex items-center gap-2">
							<span className="text-foreground opacity-70">Rows per page</span>
							<Select
								value={`${table.getState().pagination.pageSize}`}
								onValueChange={(value) => {
									table.setPageSize(Number(value));
								}}
							>
								<SelectTrigger className="w-20 h-8">
									<SelectValue />
								</SelectTrigger>
								<SelectContent side="top">
									{[10, 20, 50].map((pageSize) => (
										<SelectItem key={pageSize} value={`${pageSize}`}>
											{pageSize}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<div className="flex gap-1">
							<Button
								variant="outline"
								size="sm"
								className="h-8 px-2"
								onClick={() => table.previousPage()}
								disabled={!table.getCanPreviousPage()}
							>
								Previous
							</Button>
							<Button
								variant="outline"
								size="sm"
								className="h-8 px-2"
								onClick={() => table.nextPage()}
								disabled={!table.getCanNextPage()}
							>
								Next
							</Button>
						</div>
					</div>
				</div>
			</CardContent>
		</Card>
	);
}
