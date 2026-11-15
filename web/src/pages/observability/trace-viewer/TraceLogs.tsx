import { useTimePreferences } from "@/lib/use-time-preferences";
import { Spinner } from "@/components/design/Spinner";
import { useState } from "react";
import { LogOrder } from "@gen/loco/observability/v1/observability_pb";

import { Button } from "@/components/design/Button";

import { useEnvironments } from "@/hooks/useEnvironment";
import { useQueryLogs } from "@/hooks/useQueryLogs";
import { getErrorMessage } from "@/lib/error-handler";
import { AROUND_MS, aroundWindow } from "@/lib/obs-time";

import { formatClock, msToTimestamp } from "@/lib/time";
import { cn } from "@/lib/utils";

import { useObs } from "../context";

import { levelStyle } from "../format";
import type { Token } from "../query";
import { toRows } from "../rows";

import { type TraceSummary } from "../traces";

const NANOS_PER_MS = 1_000_000n;
const TRACE_LOG_LIMIT = 50;
function traceToken(traceId: string): Token {
	return { neg: false, key: "trace", value: traceId };
}

export function TraceLogs({ trace }: { trace: TraceSummary }) {
	const timePref = useTimePreferences();
	const { workspaceId, scopedResourceIds, resourceById, goTo } = useObs();
	const { active } = useEnvironments();
	const [now] = useState(Date.now);
	const startMs = Number(trace.start / NANOS_PER_MS);
	const endMs = startMs + Number(trace.duration / NANOS_PER_MS);
	const from = startMs - AROUND_MS;
	const to = Math.min(now, endMs + AROUND_MS);
	const logs = useQueryLogs({
		workspaceId,
		environmentId: active?.id ?? "",
		resourceIds: scopedResourceIds ?? [],
		spanMs: to - from,
		query: `@trace:${trace.id}`,
		order: LogOrder.OLDEST_FIRST,
		startTime: msToTimestamp(from),
		endTime: msToTimestamp(to),
		limit: TRACE_LOG_LIMIT,
	});
	const rows = toRows(logs.entries, resourceById);
	const more = logs.newerCursor !== "";
	return (
		<section aria-label="Logs in this trace" className="flex min-w-0 flex-col gap-1.5">
			<span className="text-[12.5px] text-fg3">
				Logs in this trace
				{rows.length > 0 && (
					<span className="ml-1.5 font-mono text-[11.5px]">{`${String(rows.length)}${more ? "+" : ""}`}</span>
				)}
			</span>
			{logs.isLoading && (
				<p role="status" className="m-0 flex items-center gap-2 text-[12px] text-fg3">
					<Spinner className="size-3.5" />
					Loading logs…
				</p>
			)}
			{!logs.isLoading && logs.error !== null && (
				<p role="alert" className="m-0 text-[12px] text-bad-fg">
					{getErrorMessage(logs.error, "Failed to load the trace's logs")}
				</p>
			)}
			{!logs.isLoading && logs.error === null && rows.length === 0 && (
				<p className="m-0 text-[12px] text-fg3">No log lines carry this trace ID.</p>
			)}
			{rows.length > 0 && (
				<ol className="m-0 flex list-none flex-col p-0">
					{rows.map((row) => (
						<li key={row.key}>
							<button
								type="button"
								onClick={() => {
									goTo("logs", {
										range: aroundWindow(row.ts, Date.now()),
										tokens: [traceToken(trace.id)],
										focusTs: row.ts,
									});
								}}
								className="grid w-full grid-cols-[88px_52px_minmax(0,1fr)] items-baseline gap-2.5 rounded-sm border-b border-line px-1.5 py-1.5 text-left font-mono text-[12px] last:border-b-0 hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
							>
								<span className="text-fg3">{formatClock(row.ts, false, timePref)}</span>
								<span className={cn("truncate", levelStyle(row.level).body)}>{row.label}</span>
								<span className="min-w-0">
									<span className="text-fg3">{row.resourceName} </span>
									<span className="[overflow-wrap:anywhere]">{row.entry.body}</span>
								</span>
							</button>
						</li>
					))}
				</ol>
			)}
			{more && (
				<Button
					variant="link"
					className="h-auto self-start p-0 text-[12px]"
					onClick={() => {
						goTo("logs", {
							range: { kind: "absolute", from, to },
							tokens: [traceToken(trace.id)],
						});
					}}
				>
					All lines in Logs
				</Button>
			)}
		</section>
	);
}
