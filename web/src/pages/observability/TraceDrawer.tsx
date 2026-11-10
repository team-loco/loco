import { useState } from "react";
import { Link } from "react-router";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import {
	BoxIcon,
	ChevronRightIcon,
	CircleAlertIcon,
	CopyIcon,
	EllipsisIcon,
	RefreshCwIcon,
	ScrollTextIcon,
	XIcon,
	ZoomOutIcon,
} from "lucide-react";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetTitle } from "@/components/design/Sheet";
import { SoonTag } from "@/components/design/SoonTag";
import { useCopy } from "@/hooks/useCopy";
import { getErrorMessage } from "@/lib/error-handler";
import { resourcePath } from "@/lib/routes";
import { cn } from "@/lib/utils";

import { SpanEvents } from "./SpanEvents";
import { useObs } from "./context";
import { failingSpan, flameLayout, FULL_ZOOM, lineage, selfTime, serviceShares, zoomTo, type Zoom } from "./flame";
import { fmtTs } from "./format";
import { traceServiceColor, traceServiceLabel } from "./trace-service";
import { axisLabel, durationLabel, spanError, spanStart, type TraceSummary } from "./traces";

const ROW_PX = 24;
const TICKS = [0, 0.2, 0.4, 0.6, 0.8, 1];
const LABEL_MIN = 0.05;
const DURATION_MIN = 0.13;
const MIN_BLOCK = 0.0025;
const PERCENT = 100;
const NANOS_PER_MS = 1_000_000n;
const TICK_SCALE = 1_000_000;

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
				["environment", r["loco.environment.name"] ?? ""],
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

function FlameGraph({
	trace,
	selected,
	onSelect,
	label,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	onSelect: (span: TraceSpan) => void;
	label: (span: TraceSpan) => string;
}) {
	const [zoom, setZoom] = useState<Zoom>(FULL_ZOOM);
	const { blocks, depth } = flameLayout(trace, zoom);
	const related = lineage(trace, selected.spanId);
	const width = zoom.b - zoom.a;
	const zoomed = width < 1;
	const shares = serviceShares(trace);
	return (
		<section aria-label="Flame graph" className="flex flex-col gap-1.5 rounded-card bg-panel px-3.5 pt-3 pb-3.5">
			<div className="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-1.5">
				<div className="mr-auto flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-fg2">
					{shares.map((s) => (
						<span key={s.service} className="flex items-center gap-1.5" title="Share of self time">
							{s.service || "Unknown service"}
							<span className="font-mono text-[11.5px] text-fg3">{Math.round(s.share * PERCENT)}%</span>
						</span>
					))}
				</div>
				{zoomed && (
					<>
						<span className="font-mono text-[12px] text-fg3">{(1 / width).toFixed(1)}×</span>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								setZoom(FULL_ZOOM);
							}}
						>
							<ZoomOutIcon />
							Reset zoom
						</Button>
					</>
				)}
			</div>
			{zoomed && (
				<div className="relative h-1 rounded-[2px] bg-line" aria-hidden="true">
					<span
						className="absolute inset-y-0 rounded-[2px] bg-fg3"
						style={{ left: `${String(zoom.a * PERCENT)}%`, width: `${String(width * PERCENT)}%` }}
					/>
				</div>
			)}
			<div className="relative h-3.5 font-mono text-[11px] text-fg3" aria-hidden="true">
				{TICKS.map((f, i) => (
					<span
						key={f}
						className={cn(
							"absolute whitespace-nowrap",
							i === 0 ? "" : i === TICKS.length - 1 ? "-translate-x-full" : "-translate-x-1/2",
						)}
						style={{ left: `${String(f * PERCENT)}%` }}
					>
						{axisLabel(
							(trace.duration * BigInt(Math.round((zoom.a + f * width) * TICK_SCALE))) / BigInt(TICK_SCALE),
							trace.duration,
						)}
					</span>
				))}
			</div>
			<div className="relative overflow-hidden" style={{ height: depth * ROW_PX }}>
				{TICKS.map((f) => (
					<span
						key={f}
						aria-hidden="true"
						className="absolute inset-y-0 border-l border-dashed border-line2"
						style={{ left: `${String(f * PERCENT)}%` }}
					/>
				))}
				{blocks.map((b) => {
					const err = spanError(b.span);
					const on = b.span.spanId === selected.spanId;
					const color = err ? "var(--err)" : traceServiceColor(b.span.serviceName);
					return (
						<button
							key={b.span.spanId}
							type="button"
							aria-pressed={on}
							aria-label={`${label(b.span)} ${b.span.name}, ${durationLabel(b.span.durationNanos)}${err ? ", error" : ""}`}
							title={`${b.span.name}\n${label(b.span)} · ${durationLabel(b.span.durationNanos)} · self ${durationLabel(selfTime(trace, b.span))}\nDouble-click to zoom`}
							onClick={() => {
								onSelect(b.span);
							}}
							onDoubleClick={() => {
								setZoom(zoomTo(trace, b.span));
							}}
							className={cn(
								"absolute flex h-[22px] items-center gap-2 overflow-hidden rounded-[3px] px-1.5 text-left text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
								err && "text-bad-fg",
								!related.has(b.span.spanId) && "opacity-45",
								on && "shadow-[inset_0_0_0_1.5px_var(--fg)]",
							)}
							style={{
								left: `${String(b.left * PERCENT)}%`,
								width: `${String(Math.max(MIN_BLOCK, b.width) * PERCENT)}%`,
								top: b.depth * ROW_PX,
								background: `color-mix(in oklab, ${color} ${err ? "26%" : "34%"}, var(--bg))`,
							}}
						>
							{b.width > LABEL_MIN && <span className="min-w-0 truncate font-mono text-[11.5px]">{b.span.name}</span>}
							{b.width > DURATION_MIN && (
								<span className="ml-auto font-mono text-[11px] whitespace-nowrap opacity-70">
									{durationLabel(b.span.durationNanos)}
								</span>
							)}
						</button>
					);
				})}
			</div>
			<span className="text-[11.5px] text-fg3">Select a span for details. Double-click a span to zoom into it.</span>
		</section>
	);
}

export function TraceDrawer({
	traceId,
	trace,
	loading,
	error,
	incomplete,
	loadingMore,
	onLoadMore,
	onRetry,
	onClose,
}: {
	traceId: string;
	trace: TraceSummary | undefined;
	loading: boolean;
	error: Error | null;
	incomplete: boolean;
	loadingMore: boolean;
	onLoadMore: () => void;
	onRetry: () => void;
	onClose: () => void;
}) {
	const { orgId, workspaceId, resourceById, params, setParam } = useObs();
	const [copied, copy] = useCopy();
	const spanId = params.get("span");
	const linked = trace?.spans.find((s) => s.spanId === spanId);
	const selected = linked ?? trace?.root;
	const missing = spanId !== null && trace !== undefined && linked === undefined;
	const label = (span: TraceSpan) => traceServiceLabel(span, resourceById.get(span.resourceId)?.name);
	const resource = trace === undefined ? undefined : resourceById.get(trace.root.resourceId);
	const cause = trace !== undefined && selected !== undefined ? failingSpan(trace, selected) : undefined;
	const failed = trace !== undefined && trace.errors > 0;
	const select = (span: TraceSpan) => {
		setParam("span", span.spanId, true);
	};
	return (
		<Sheet
			open
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<SheetContent size="full" showCloseButton={false}>
				<div className="flex shrink-0 items-start justify-between gap-3 border-b border-line py-4 pr-4 pl-6">
					<div className="flex min-w-0 flex-col gap-1">
						<div className="flex min-w-0 items-center gap-2.5">
							<SheetTitle className="truncate font-mono">
								{trace === undefined || trace.root.name === "" ? "Trace" : trace.root.name}
							</SheetTitle>
							{trace !== undefined && (
								<Badge tone={failed ? "bad" : "muted"} className="font-mono">
									{failed ? "Error" : "OK"}
								</Badge>
							)}
							{trace !== undefined && (
								<span className="font-mono text-[12.5px] text-fg2" title="Observed from the loaded spans">
									{durationLabel(trace.duration)}
								</span>
							)}
						</div>
						<SheetDescription className="truncate font-mono text-[12px]">
							{trace === undefined
								? traceId
								: [
										label(trace.root),
										fmtTs(Number(trace.start / NANOS_PER_MS), true),
										`${String(trace.spans.length)} loaded ${trace.spans.length === 1 ? "span" : "spans"}`,
									].join(" · ")}
						</SheetDescription>
					</div>
					<div className="flex shrink-0 items-center gap-1">
						<DropdownMenu>
							<DropdownMenuTrigger
								render={<Button variant="ghost" size="icon-sm" aria-label="More for this trace" title="More" />}
							>
								<EllipsisIcon className="size-4" />
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" className="w-[250px]">
								<DropdownMenuItem
									onClick={() => {
										copy("trace", traceId);
									}}
								>
									<CopyIcon />
									{copied === "trace" ? "Copied" : "Copy trace ID"}
								</DropdownMenuItem>
								{resource !== undefined && (
									<DropdownMenuItem render={<Link to={resourcePath(orgId, workspaceId, resource.id)} />}>
										<BoxIcon />
										Open {resource.name}
									</DropdownMenuItem>
								)}
								<DropdownMenuItem disabled>
									<ScrollTextIcon />
									Logs for this trace
									<span className="ml-auto">
										<SoonTag />
									</span>
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
						<Button variant="ghost" size="icon-sm" aria-label="Close" onClick={onClose}>
							<XIcon className="size-4" />
						</Button>
					</div>
				</div>
				<SheetBody className="gap-4">
					{loading && (
						<div role="status" className="flex min-h-[200px] items-center justify-center gap-2 text-fg3">
							<RefreshCwIcon className="size-4 animate-spin motion-reduce:animate-none" />
							Loading spans…
						</div>
					)}
					{!loading && error !== null && (
						<div role="alert" className="flex flex-wrap items-center justify-between gap-3 py-6">
							<span className="text-bad-fg">{getErrorMessage(error, "Failed to load the trace")}</span>
							<Button variant="outline" onClick={onRetry}>
								Retry
							</Button>
						</div>
					)}
					{!loading && error === null && trace === undefined && (
						<p className="py-6 text-fg3">No spans for this trace in the selected time range.</p>
					)}
					{trace !== undefined && selected !== undefined && (
						<>
							<FlameGraph trace={trace} selected={selected} onSelect={select} label={label} />
							{missing && (
								<p role="status" className="m-0 text-[12.5px] text-fg2">
									{incomplete
										? "The selected span has not loaded. Load more spans to find it."
										: "The selected span is unavailable in this time range."}
								</p>
							)}
							{incomplete && (
								<div className="flex flex-wrap items-center justify-between gap-2 text-[12px] text-fg3">
									<span>More spans exist than were loaded. Durations and shares reflect the loaded spans.</span>
									<Button variant="outline" size="sm" disabled={loadingMore} onClick={onLoadMore}>
										{loadingMore ? "Loading…" : "Load more spans"}
									</Button>
								</div>
							)}
							<section className="flex min-w-0 items-center gap-2.5" aria-label="Selected span">
								<h3 className="m-0 min-w-0 truncate font-mono text-[14px] font-medium">
									{selected.name || "Unnamed span"}
								</h3>
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
							<SpanEvents events={selected.events} />
							<section aria-label="Span attributes" className="flex flex-col gap-3.5">
								{groupAttributes(selected, trace, resourceById.get(selected.resourceId)?.name).map((g) => (
									<div key={g.title} className="flex flex-col">
										<span className="pb-1 text-[12.5px] text-fg3">{g.title}</span>
										<dl className="m-0">
											{g.rows.map(([k, v]) => (
												<div
													key={k}
													className="grid grid-cols-[150px_minmax(0,1fr)] gap-2.5 border-b border-line py-1.5 text-[12px] last:border-b-0"
												>
													<dt className="truncate font-mono text-fg3" title={k}>
														{k}
													</dt>
													<dd className="m-0 font-mono break-all text-foreground">{v}</dd>
												</div>
											))}
										</dl>
									</div>
								))}
							</section>
							<section aria-label="Logs in this trace" className="flex flex-col gap-1.5">
								<span className="flex items-center gap-1.5 text-[12.5px] text-fg3">
									Logs in this trace
									<SoonTag />
								</span>
								<p className="m-0 text-[12px] text-fg3">Log queries can't filter by trace ID yet.</p>
							</section>
						</>
					)}
				</SheetBody>
			</SheetContent>
		</Sheet>
	);
}
