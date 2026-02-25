import { useState } from "react";
import { ChevronRight, ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";
import type { LogEntry } from "@/gen/loco/observability/v1/observability_pb";

const SEVERITY_STYLES: Record<string, string> = {
	DEBUG: "text-muted-foreground",
	INFO: "text-blue-500",
	WARN: "text-yellow-500",
	WARNING: "text-yellow-500",
	ERROR: "text-red-500",
	FATAL: "text-red-600 font-semibold",
};

function formatTimestamp(ts: { seconds: bigint; nanos: number } | undefined): string {
	if (!ts) return "—";
	const d = new Date(Number(ts.seconds) * 1000 + ts.nanos / 1e6);
	return d.toLocaleString("en-US", {
		month: "short",
		day: "2-digit",
		hour: "2-digit",
		minute: "2-digit",
		second: "2-digit",
		hour12: false,
	});
}

export function ObsLogRow({ entry }: { entry: LogEntry }) {
	const [expanded, setExpanded] = useState(false);
	const severity = entry.severity ?? "INFO";
	const severityClass =
		SEVERITY_STYLES[severity.toUpperCase()] ??
		"text-muted-foreground";

	const hasExtra =
		Object.keys(entry.resourceAttributes).length > 0 ||
		Object.keys(entry.logAttributes).length > 0 ||
		(entry.traceId && entry.traceId.length > 0) ||
		(entry.spanId && entry.spanId.length > 0);

	return (
		<div
			className={cn(
				"font-mono text-xs border-b border-border/40 last:border-0",
				expanded && "bg-muted/30",
			)}
		>
			{/* Main row */}
			<div
				className="flex items-start gap-2 px-3 py-1.5 hover:bg-muted/20 cursor-pointer select-text"
				onClick={() => {
					if (hasExtra) setExpanded((v) => !v);
				}}
			>
				<span className="shrink-0 text-muted-foreground/60 w-36 tabular-nums">
					{formatTimestamp(entry.timestamp)}
				</span>
				<span className={cn("shrink-0 w-12 uppercase", severityClass)}>
					{(entry.severity ?? "INFO").slice(0, 5)}
				</span>
				<span className="flex-1 break-all leading-relaxed">{entry.body}</span>
				{hasExtra && (
					<span className="shrink-0 text-muted-foreground/40 mt-0.5">
						{expanded ? (
							<ChevronDown className="h-3 w-3" />
						) : (
							<ChevronRight className="h-3 w-3" />
						)}
					</span>
				)}
			</div>

			{/* Expanded attributes */}
			{expanded && hasExtra && (
				<div className="px-3 pb-2 pl-[11.5rem] text-muted-foreground space-y-0.5">
					{entry.traceId && (
						<div>
							<span className="text-muted-foreground/60">trace_id: </span>
							{entry.traceId}
						</div>
					)}
					{entry.spanId && (
						<div>
							<span className="text-muted-foreground/60">span_id: </span>
							{entry.spanId}
						</div>
					)}
					{Object.entries(entry.resourceAttributes).map(([k, v]) => (
						<div key={k}>
							<span className="text-muted-foreground/60">{k}: </span>
							{v}
						</div>
					))}
					{Object.entries(entry.logAttributes).map(([k, v]) => (
						<div key={k}>
							<span className="text-muted-foreground/60">{k}: </span>
							{v}
						</div>
					))}
				</div>
			)}
		</div>
	);
}
