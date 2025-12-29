import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
	Tooltip,
	TooltipContent,
	TooltipProvider,
	TooltipTrigger,
} from "@/components/ui/tooltip";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import {
	DeploymentPhase,
	type Deployment,
} from "@/gen/deployment/v1/deployment_pb";
import {
	flexRender,
	getCoreRowModel,
	getPaginationRowModel,
	getSortedRowModel,
	useReactTable,
	type ColumnDef,
	type SortingState,
} from "@tanstack/react-table";
import { ChevronDown, ChevronUp, RotateCcw, ArrowUpDown } from "lucide-react";
import React, { useState } from "react";
import { PHASE_COLOR_MAP } from "@/lib/deployment-constants";
import { getServiceSpec, getPhaseTooltip } from "@/lib/deployment-utils";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";

interface RecentDeploymentsProps {
	deployments: Deployment[];
	appId?: string;
	isLoading?: boolean;
}

export function RecentDeployments({
	deployments,
	isLoading = false,
}: RecentDeploymentsProps) {
	const [sorting, setSorting] = useState<SortingState>([
		{ id: "createdAt", desc: true },
	]);
	const [pagination, setPagination] = useState({
		pageIndex: 0,
		pageSize: 10,
	});
	const [expandedId, setExpandedId] = useState<bigint | null>(null);

	const formatTimestamp = (timestamp: unknown): string => {
		if (!timestamp) return "unknown";
		try {
			let ms: number;
			if (typeof timestamp === "object" && "seconds" in timestamp) {
				ms = Number((timestamp as Record<string, unknown>).seconds) * 1000;
			} else if (typeof timestamp === "number") {
				ms = timestamp;
			} else {
				return "unknown";
			}
			const date = new Date(ms);
			return date.toLocaleDateString("en-US", {
				month: "short",
				day: "numeric",
				hour: "2-digit",
				minute: "2-digit",
				hour12: true,
			});
		} catch {
			return "unknown";
		}
	};

	const getPhaseColor = (phase: DeploymentPhase): string => {
		return PHASE_COLOR_MAP[phase];
	};

	const getImage = (deployment: Deployment): string => {
		const service = getServiceSpec(deployment);
		return service?.build?.image || "—";
	};

	const getRegion = (deployment: Deployment): string => {
		const service = getServiceSpec(deployment);
		return service?.region || "—";
	};

	const columns: ColumnDef<Deployment>[] = [
		{
			id: "expand",
			header: () => null,
			cell: ({ row }) => (
				<button
					onClick={() =>
						setExpandedId(
							expandedId === row.original.id ? null : row.original.id
						)
					}
					className="p-0 h-6 w-6 flex items-center justify-center hover:bg-accent/50 rounded transition-colors"
				>
					{expandedId === row.original.id ? (
						<ChevronUp className="w-4 h-4" />
					) : (
						<ChevronDown className="w-4 h-4" />
					)}
				</button>
			),
			size: 40,
		},
		{
			accessorKey: "id",
			header: ({ column }) => (
				<button
					onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
					className="flex items-center gap-2 hover:bg-accent/50 px-2 py-1 rounded transition-colors"
				>
					Deployment ID
					<ArrowUpDown className="h-3 w-3" />
				</button>
			),
			cell: ({ row }) => (
				<span className="font-mono text-xs max-w-xs truncate">
					{row.original.id.toString()}
				</span>
			),
			size: 150,
		},
		{
			accessorKey: "status",
			header: ({ column }) => (
				<button
					onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
					className="flex items-center gap-2 hover:bg-accent/50 px-2 py-1 rounded transition-colors"
				>
					Status
					<ArrowUpDown className="h-3 w-3" />
				</button>
			),
			cell: ({ row }) => (
				<TooltipProvider>
					<Tooltip>
						<TooltipTrigger asChild>
							<Badge
								variant="default"
								className={`text-xs ${getPhaseColor(row.original.status)}`}
							>
								{DeploymentPhase[row.original.status]}
							</Badge>
						</TooltipTrigger>
						<TooltipContent>
							{getPhaseTooltip(row.original.status)}
						</TooltipContent>
					</Tooltip>
				</TooltipProvider>
			),
			size: 120,
		},
		{
			accessorKey: "replicas",
			header: ({ column }) => (
				<button
					onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
					className="flex items-center gap-2 hover:bg-accent/50 px-2 py-1 rounded transition-colors"
				>
					Replicas
					<ArrowUpDown className="h-3 w-3" />
				</button>
			),
			cell: ({ row }) => (
				<span className="text-sm">{row.original.replicas || "—"}</span>
			),
			size: 100,
		},
		{
			id: "region",
			header: () => <span>Region</span>,
			cell: ({ row }) => (
				<span className="text-sm font-mono">{getRegion(row.original)}</span>
			),
			size: 120,
		},
		{
			accessorKey: "createdAt",
			header: ({ column }) => (
				<button
					onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
					className="flex items-center gap-2 hover:bg-accent/50 px-2 py-1 rounded transition-colors"
				>
					Created
					<ArrowUpDown className="h-3 w-3" />
				</button>
			),
			cell: ({ row }) => (
				<span className="text-sm text-foreground opacity-70">
					{formatTimestamp(row.original.createdAt)}
				</span>
			),
			size: 150,
		},
		{
			id: "actions",
			header: () => <div className="text-right">Actions</div>,
			cell: () => (
				<div className="text-right">
					<Button
						variant="secondary"
						size="sm"
						className="h-7 text-[11px]"
						disabled
						onClick={(e) => {
							e.stopPropagation();
						}}
					>
						<RotateCcw className="w-3 h-3 mr-1" />
						Rollback
					</Button>
				</div>
			),
			size: 120,
		},
	];

	const table = useReactTable({
		data: deployments,
		columns,
		state: {
			sorting,
			pagination,
		},
		onSortingChange: setSorting,
		onPaginationChange: setPagination,
		getCoreRowModel: getCoreRowModel(),
		getSortedRowModel: getSortedRowModel(),
		getPaginationRowModel: getPaginationRowModel(),
		getRowId: (row) => row.id.toString(),
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

	if (deployments.length === 0) {
		return (
			<Card className="border-2">
				<CardHeader>
					<CardTitle>Previous Deployments</CardTitle>
				</CardHeader>
				<CardContent>
					<p className="text-sm text-foreground opacity-70">
						No previous deployments
					</p>
				</CardContent>
			</Card>
		);
	}

	return (
		<Card className="border-2">
			<CardHeader>
				<CardTitle>Previous Deployments ({deployments.length})</CardTitle>
			</CardHeader>
			<CardContent className="space-y-4">
				{/* Table */}
				<div className="overflow-hidden rounded-lg border">
					<Table>
						<TableHeader className="sticky top-0 z-10 bg-muted">
							{table.getHeaderGroups().map((headerGroup) => (
								<TableRow key={headerGroup.id}>
									{headerGroup.headers.map((header) => (
										<TableHead
											key={header.id}
											style={{
												width: header.getSize(),
											}}
										>
											{header.isPlaceholder
												? null
												: flexRender(
														header.column.columnDef.header,
														header.getContext()
												  )}
										</TableHead>
									))}
								</TableRow>
							))}
						</TableHeader>
						<TableBody>
							{table.getRowModel().rows?.length ? (
								table.getRowModel().rows.map((row) => (
									<React.Fragment key={row.id}>
										<TableRow className="cursor-pointer hover:bg-background/50">
											{row.getVisibleCells().map((cell) => (
												<TableCell
													key={cell.id}
													style={{
														width: cell.column.getSize(),
													}}
												>
													{flexRender(
														cell.column.columnDef.cell,
														cell.getContext()
													)}
												</TableCell>
											))}
										</TableRow>

										{/* Expanded Details */}
										{expandedId === row.original.id && (
											<TableRow
												key={`${row.id}-expanded`}
												className="bg-background/30"
											>
												<TableCell colSpan={columns.length}>
													<div className="p-4 space-y-3">
														<div>
															<p className="text-xs text-foreground opacity-60 uppercase">
																ID
															</p>
															<p className="text-sm font-mono">
																{row.original.id.toString()}
															</p>
														</div>
														<div>
															<p className="text-xs text-foreground opacity-60 uppercase">
																Image
															</p>
															<p className="text-sm font-mono break-all">
																{getImage(row.original)}
															</p>
														</div>
														<div className="grid grid-cols-3 gap-4">
															<div>
																<p className="text-xs text-foreground opacity-60 uppercase">
																	Replicas
																</p>
																<p className="text-sm">
																	{row.original.replicas || "—"}
																</p>
															</div>
															<div>
																<p className="text-xs text-foreground opacity-60 uppercase">
																	Region
																</p>
																<p className="text-sm font-mono">
																	{getRegion(row.original)}
																</p>
															</div>
															<div>
																<p className="text-xs text-foreground opacity-60 uppercase">
																	Status
																</p>
																<p className="text-sm">
																	{DeploymentPhase[row.original.status]}
																</p>
															</div>
														</div>
														{row.original.message && (
															<div>
																<p className="text-xs text-foreground opacity-60 uppercase">
																	Message
																</p>
																<p className="text-sm break-all">
																	{row.original.message}
																</p>
															</div>
														)}
													</div>
												</TableCell>
											</TableRow>
										)}
									</React.Fragment>
								))
							) : (
								<TableRow>
									<TableCell
										colSpan={columns.length}
										className="h-24 text-center"
									>
										No deployments.
									</TableCell>
								</TableRow>
							)}
						</TableBody>
					</Table>
				</div>

				{/* Pagination */}
				<div className="flex items-center justify-between">
					<div className="text-sm text-foreground opacity-70">
						Page {table.getState().pagination.pageIndex + 1} of{" "}
						{table.getPageCount()}
					</div>
					<div className="flex items-center gap-2">
						<div className="hidden sm:flex items-center gap-2">
							<span className="text-sm text-foreground opacity-70">
								Rows per page
							</span>
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
									{[5, 10, 20, 30].map((pageSize) => (
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
