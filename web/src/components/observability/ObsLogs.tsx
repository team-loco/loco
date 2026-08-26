import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { Toggle } from "@/components/ui/toggle";
import { LogOrder } from "@gen/loco/observability/v1/observability_pb";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { useTailLogs } from "@/hooks/useTailLogs";
import { parseObsQuery } from "@/lib/obs-query-parser";
import { AlertCircle, Loader2, Radio, Search } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { ObsLogRow } from "./ObsLogRow";
import { useObs } from "./ObsProvider";

function levelBadgeVariant(
	level: string,
): "default" | "outline" | "destructive" | "secondary" {
	switch (level) {
		case "ERROR":
			return "destructive";
		case "FATAL":
			return "destructive";
		default:
			return "outline";
	}
}

export function ObsLogs() {
	const { clusterTransports, workspaceId, selectedResourceIds, timeRange } =
		useObs();

	const [queryText, setQueryText] = useState("");
	const [liveMode, setLiveMode] = useState(false);
	const [order, setOrder] = useState<LogOrder>(LogOrder.NEWEST_FIRST);

	const parsedQuery = useMemo(() => parseObsQuery(queryText), [queryText]);

	// Paginated cursor per cluster (map of clusterId → cursor)
	const [cursors, setCursors] = useState<Record<string, string>>({});

	const { mergedEntries, clusterLogs, isLoading, errors } = useQueryLogs({
		clusterTransports,
		workspaceId,
		resourceIds: selectedResourceIds,
		timeRange,
		parsedQuery,
		order,
		limit: 200,
		enabled: !liveMode,
	});

	// Live tail — only active when liveMode is on
	const { entries: tailEntries, isConnected } = useTailLogs({
		clusterTransports,
		workspaceId,
		resourceIds: selectedResourceIds,
		parsedQuery,
		enabled: liveMode,
	});

	const displayEntries = liveMode ? tailEntries : mergedEntries;
	const isEmpty = !isLoading && displayEntries.length === 0;

	// Load more: advance cursor for each cluster that has more
	const hasMore = clusterLogs.some((cl) => cl.data?.nextCursor);
	const loadMore = useCallback(() => {
		const next: Record<string, string> = { ...cursors };
		for (const cl of clusterLogs) {
			if (cl.data?.nextCursor) {
				next[cl.clusterId] = cl.data.nextCursor;
			}
		}
		setCursors(next);
	}, [clusterLogs, cursors]);

	return (
		<div className="flex flex-col gap-3">
			{/* Query bar */}
			<div className="flex items-center gap-2 justify-between">
				<div className="relative w-96">
					<Search className="absolute left-2.5 top-2 h-3.5 w-3.5 text-muted-foreground" />
					<Input
						value={queryText}
						onChange={(e) => {
							setQueryText(e.target.value);
						}}
						placeholder='level:error pod:api-xxx "search text"'
						className="pl-8 h-8 text-sm"
					/>
				</div>
				<div className="flex items-center gap-2">
					<Toggle
						pressed={order === LogOrder.OLDEST_FIRST}
						onPressedChange={(v) => {
							setOrder(v ? LogOrder.OLDEST_FIRST : LogOrder.NEWEST_FIRST);
						}}
						size="sm"
						aria-label="Toggle order"
						className="text-xs bg-accent"
					>
						{order === LogOrder.NEWEST_FIRST ? "Newest first" : "Oldest first"}
					</Toggle>
					<Toggle
						pressed={liveMode}
						onPressedChange={setLiveMode}
						size="sm"
						aria-label="Live tail"
						className="gap-1.5 text-xs"
						style={{
							backgroundColor: liveMode ? "var(--primary)" : "var(--accent)",
							color: liveMode ? "var(--primary-foreground)" : "inherit",
						}}
					>
						<Radio className="h-3.5 w-3.5" />
						Live
						{liveMode && isConnected && (
							<span className="h-1.5 w-1.5 rounded-full bg-green-500 animate-pulse" />
						)}
					</Toggle>
				</div>
			</div>

			{/* Active level filters parsed from query */}
			{parsedQuery.levels.length > 0 && (
				<div className="flex items-center gap-1.5">
					<span className="text-xs text-muted-foreground">Level:</span>
					{parsedQuery.levels.map((lvl) => (
						<Badge
							key={lvl}
							variant={levelBadgeVariant(lvl)}
							className="text-xs"
						>
							{lvl}
						</Badge>
					))}
				</div>
			)}

			{/* Log table */}
			<div className="rounded-md border border-border overflow-hidden bg-card">
				{isLoading && !liveMode && (
					<div className="flex items-center justify-center py-10 gap-2 text-sm text-muted-foreground">
						<Loader2 className="h-4 w-4 animate-spin" />
						Loading logs…
					</div>
				)}

				{!isLoading && isEmpty && (
					<div className="flex flex-col items-center justify-center py-12 gap-2 text-sm text-muted-foreground">
						<AlertCircle className="h-5 w-5" />
						No logs found for the selected filters and time range.
					</div>
				)}

				{errors.length > 0 && (
					<div className="px-3 py-2 text-xs text-destructive border-b border-border/40">
						{errors.map((e) => (
							<div key={e.name}>{e.message}</div>
						))}
					</div>
				)}

				{displayEntries.length > 0 && (
					<div>
						{displayEntries.map((entry, i) => (
							<ObsLogRow
								key={`${entry.timestamp?.seconds.toString() ?? ""}-${entry.traceId}-${i.toString()}`}
								entry={entry}
							/>
						))}
					</div>
				)}
			</div>

			{/* Load more */}
			{!liveMode && hasMore && !isLoading && (
				<div className="flex justify-center">
					<Button variant="outline" size="sm" onClick={loadMore}>
						Load more
					</Button>
				</div>
			)}
		</div>
	);
}
