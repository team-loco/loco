import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuCheckboxItem,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Clock, ChevronDown, Layers } from "lucide-react";
import { useObs, type TimeRange } from "./ObsProvider";

const TIME_RANGE_OPTIONS: { value: TimeRange; label: string }[] = [
	{ value: "15m", label: "Last 15 min" },
	{ value: "1h", label: "Last 1 hour" },
	{ value: "3h", label: "Last 3 hours" },
	{ value: "6h", label: "Last 6 hours" },
	{ value: "24h", label: "Last 24 hours" },
	{ value: "7d", label: "Last 7 days" },
];

export function ObsToolbar() {
	const {
		resources,
		clusters,
		selectedResourceIds,
		setSelectedResourceIds,
		activeClusterIds,
		setActiveClusterIds,
		timeRange,
		setTimeRange,
	} = useObs();

	const toggleResource = (id: string) => {
		setSelectedResourceIds(
			selectedResourceIds.includes(id)
				? selectedResourceIds.filter((r) => r !== id)
				: [...selectedResourceIds, id],
		);
	};

	const toggleCluster = (id: string) => {
		setActiveClusterIds(
			activeClusterIds.includes(id)
				? activeClusterIds.filter((c) => c !== id)
				: [...activeClusterIds, id],
		);
	};

	const resourceLabel =
		selectedResourceIds.length === 0
			? "All resources"
			: selectedResourceIds.length === 1
				? (resources.find((r) => r.id === selectedResourceIds[0])?.name ??
					"1 resource")
				: `${selectedResourceIds.length.toString()} resources`;

	const timeRangeLabel =
		TIME_RANGE_OPTIONS.find((opt) => opt.value === timeRange)?.label ??
		timeRange;

	return (
		<div className="flex items-center gap-2 flex-wrap">
			{/* Resource picker */}
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button variant="outline" size="sm" className="gap-1.5">
						<Layers className="h-3.5 w-3.5" />
						{resourceLabel}
						<ChevronDown className="h-3.5 w-3.5 opacity-60" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" className="w-56">
					<DropdownMenuLabel>Filter by resource</DropdownMenuLabel>
					<DropdownMenuSeparator />
					<DropdownMenuCheckboxItem
						checked={selectedResourceIds.length === 0}
						onCheckedChange={() => {
							setSelectedResourceIds([]);
						}}
						className="cursor-pointer"
					>
						All resources
					</DropdownMenuCheckboxItem>
					<DropdownMenuSeparator />
					{resources.map((r) => (
						<DropdownMenuCheckboxItem
							key={r.id}
							checked={selectedResourceIds.includes(r.id)}
							onCheckedChange={() => toggleResource(r.id)}
							className="cursor-pointer"
						>
							{r.name}
						</DropdownMenuCheckboxItem>
					))}
				</DropdownMenuContent>
			</DropdownMenu>

			{/* Time range */}
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button variant="outline" size="sm" className="gap-1.5">
						<Clock className="h-3.5 w-3.5" />
						{timeRangeLabel}
						<ChevronDown className="h-3.5 w-3.5 opacity-60" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" className="w-40">
					{TIME_RANGE_OPTIONS.map((opt) => (
						<DropdownMenuItem
							key={opt.value}
							onClick={() => setTimeRange(opt.value)}
							className="cursor-pointer"
						>
							{opt.label}
						</DropdownMenuItem>
					))}
				</DropdownMenuContent>
			</DropdownMenu>

			{/* Region pills — only shown when there are multiple clusters */}
			{clusters.length > 1 && (
				<div className="flex items-center gap-1.5">
					<Badge
						variant={activeClusterIds.length === 0 ? "default" : "outline"}
						className="cursor-pointer text-xs"
						onClick={() => {
							setActiveClusterIds([]);
						}}
					>
						All regions
					</Badge>
					{clusters.map((c) => {
						const id = c.clusterId.toString();
						const active = activeClusterIds.includes(id);
						return (
							<Badge
								key={id}
								variant={active ? "default" : "outline"}
								className="cursor-pointer text-xs"
								onClick={() => {
									toggleCluster(id);
								}}
							>
								{c.region}
							</Badge>
						);
					})}
				</div>
			)}
		</div>
	);
}
