import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ChevronRightIcon, CircleAlertIcon } from "lucide-react";

import { cn } from "@/lib/utils";

import { SpanEvents } from "../SpanEvents";
import { SpanLinks } from "../SpanLinks";
import { useObs } from "../context";
import { failingSpan, selfTime } from "../flame";

import { durationLabel, spanError, spanStart, type TraceSummary } from "../traces";

const PERCENT = 100;
const NAMESPACE_TITLES: Record<string, string> = {
	http: "HTTP",
	url: "HTTP",
	db: "Database",
	rpc: "RPC",
	messaging: "Messaging",
	net: "Network",
	network: "Network",
	server: "Network",
	client: "Network",
	peer: "Network",
	exception: "Exception",
	code: "Code",
};

interface AttributeGroup {
	title: string;
	rows: [string, string][];
}

function groupAttributes(span: TraceSpan, trace: TraceSummary, resourceName: string | undefined): AttributeGroup[] {
	const r = span.resourceAttributes;
	const groups: AttributeGroup[] = [
		{
			title: "Loco",
			rows: [
				["resource", resourceName ?? r["service.name"] ?? span.serviceName],
				["replica", r["k8s.pod.name"] ?? ""],
				["region", r["loco.region"] ?? ""],
				["environment", r["deployment.environment.name"] ?? ""],
			],
		},
		{
			title: "Span",
			rows: [
				["duration", durationLabel(span.durationNanos)],
				["self time", durationLabel(selfTime(trace, span))],
				["offset", durationLabel(spanStart(span) - trace.start)],
				["kind", span.kind],
				["status", span.statusCode],
				["span id", span.spanId],
				["parent", span.parentSpanId === "" ? "root span" : span.parentSpanId],
			],
		},
	];
	const byTitle = new Map<string, [string, string][]>();
	for (const [k, v] of Object.entries(span.spanAttributes)) {
		const ns = k.split(".")[0] ?? "";
		const title = k.includes(".") ? (NAMESPACE_TITLES[ns] ?? "Other") : "Other";
		byTitle.set(title, [...(byTitle.get(title) ?? []), [k, v]]);
	}
	for (const [title, rows] of byTitle) groups.push({ title, rows });
	groups.push({
		title: "Resource",
		rows: Object.entries(r).filter(([k]) => k !== "k8s.pod.name" && k !== "loco.region"),
	});
	groups.push({
		title: "Instrumentation",
		rows: [
			["scope", span.scopeName],
			["version", span.scopeVersion],
		],
	});
	return groups.map((g) => ({ ...g, rows: g.rows.filter(([, v]) => v !== "") })).filter((g) => g.rows.length > 0);
}

export function SpanPanel({
	trace,
	selected,
	missing,
	select,
	label,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	missing: boolean;
	select: (span: TraceSpan) => void;
	label: (span: TraceSpan) => string;
}) {
	const { resourceById } = useObs();
	const cause = failingSpan(trace, selected);
	return (
		<aside className="flex min-w-0 flex-col gap-4 border-line min-[1100px]:border-l min-[1100px]:pl-5">
			{" "}
			{missing && (
				<p role="status" className="m-0 text-[12.5px] text-fg2">
					The selected span is unavailable.
				</p>
			)}
			<section className="flex min-w-0 items-center gap-2.5" aria-label="Selected span">
				<h3 className="m-0 min-w-0 truncate font-mono text-[14px] font-medium">{selected.name || "Unnamed span"}</h3>
				<span className="text-[12.5px] text-fg3">{label(selected)}</span>
				<span className="flex-1" />
				<span className="font-mono text-[12.5px]">{durationLabel(selected.durationNanos)}</span>
				<span className="font-mono text-[12px] text-fg3">
					{trace.duration > 0n
						? `${String(Number((selected.durationNanos * BigInt(PERCENT)) / trace.duration))}% of trace`
						: ""}
				</span>
			</section>
			{cause !== undefined && (
				<button
					type="button"
					onClick={() => {
						select(cause);
					}}
					className="flex items-center gap-2 rounded-lg bg-bad-bg px-3 py-2.5 text-left text-[13px] shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--err)_25%,transparent)] hover:brightness-[0.98] focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
				>
					<CircleAlertIcon className="size-4 text-bad-fg" />
					Failed in
					<span className="font-mono">{cause.name || "Unnamed span"}</span>
					<span className="text-fg3">{label(cause)}</span>
					<ChevronRightIcon className="ml-auto size-4 text-fg3" />
				</button>
			)}
			{selected.statusMessage !== "" && (
				<pre
					className={cn(
						"m-0 rounded-lg px-3.5 py-3 font-mono text-[12px] leading-[1.55] break-words whitespace-pre-wrap",
						spanError(selected)
							? "bg-bad-bg text-bad-fg shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--err)_25%,transparent)]"
							: "bg-panel text-foreground",
					)}
				>
					{selected.statusMessage}
				</pre>
			)}
			<SpanEvents span={selected} />
			<SpanLinks span={selected} />
			<div className="min-w-0">
				<section aria-label="Span attributes" className="flex min-w-0 flex-col gap-3.5">
					{groupAttributes(selected, trace, resourceById.get(selected.resourceId)?.name).map((g) => (
						<div key={g.title} className="flex flex-col">
							<span className="pb-1 text-[12.5px] text-fg3">{g.title}</span>
							<dl className="m-0">
								{g.rows.map(([k, v]) => (
									<div
										key={k}
										className="grid grid-cols-[minmax(0,35%)_minmax(0,1fr)] gap-2.5 border-b border-line py-1.5 text-[12px] last:border-b-0"
									>
										<dt className="font-mono break-words text-fg3 [overflow-wrap:anywhere]">{k}</dt>
										<dd className="m-0 min-w-0 font-mono text-foreground [overflow-wrap:anywhere]">{v}</dd>
									</div>
								))}
							</dl>
						</div>
					))}
				</section>
			</div>
		</aside>
	);
}
