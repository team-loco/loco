import { useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { ChevronDownIcon, ChevronRightIcon, CircleAlertIcon, XIcon } from "lucide-react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

import { Button } from "@/components/design/Button";
import { SearchInput } from "@/components/design/SearchInput";
import { cn } from "@/lib/utils";

import { axisLabel, durationLabel, spanError, spanPosition, spanStart, spanTree, type TraceSummary } from "./traces";

const TICKS = [0, 50, 100];
const MAX_INDENT = 8;
const DETAILS_SIDE_BY_SIDE = "(min-width: 80rem)";

export function TraceWaterfall({ trace, incomplete }: { trace: TraceSummary; incomplete: boolean; }) {
	const [params, setParams] = useSearchParams();
	const [collapsed, setCollapsed] = useState(() => new Set<string>());
	const requestedSpan = params.get("span");
	const selected = trace.spans.find((span) => span.spanId === requestedSpan);
	const rows = spanTree(trace.spans, collapsed);
	const triggerRef = useRef<HTMLButtonElement | null>(null);
	const select = (id: string | null, target?: HTMLButtonElement) => {
		if (target !== undefined) triggerRef.current = target;
		if (id === null) triggerRef.current?.focus();
		const next = new URLSearchParams(params);
		if (id === null) next.delete("span"); else next.set("span", id);
		setParams(next, { replace: true });
	};
	return <div className={cn("grid min-w-0 items-start", selected !== undefined && "xl:grid-cols-[minmax(0,1fr)_320px]")}>
		<div className="min-w-0">
			<div className="flex items-center justify-between gap-3 border-b border-line px-4 py-2.5">
				<h3 className="font-medium">Span waterfall</h3>
				<Button variant="ghost" size="xs" disabled={collapsed.size === 0} onClick={() => { setCollapsed(new Set()); }}>Expand all</Button>
			</div>
			{requestedSpan !== null && selected === undefined && <div role="status" className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-3 text-sm text-fg3"><span>{incomplete ? "The selected span has not loaded. Load more spans to find it." : "The selected span is unavailable in this time range."}</span><Button variant="ghost" size="xs" onClick={() => { select(null); }}>Clear span selection</Button></div>}
			<div className="overflow-x-auto">
				<div className="min-w-0 sm:min-w-[660px]">
					<div className="grid grid-cols-1 sm:grid-cols-[minmax(240px,38%)_1fr] border-b border-line bg-bg2 px-4 text-sm text-fg3">
						<span className="hidden py-3 sm:block">Service / operation</span>
						<div className="relative mx-6 h-10">{TICKS.map((tick) => <span
							key={tick}
							className={cn("absolute top-3 font-mono text-[10px] whitespace-nowrap", tick === 100 ? "-translate-x-full" : tick > 0 ? "-translate-x-1/2" : "")}
							style={{ left: `${String(tick)}%` }}>{axisLabel(trace.duration * BigInt(tick) / 100n, trace.duration)}
						</span>)}
						</div>
					</div>
					{rows.map(({ span, depth, children, missingParent }) => {
						const error = spanError(span);
						const active = selected?.spanId === span.spanId;
						return <div
							key={span.spanId}
							className={cn("grid grid-cols-1 sm:grid-cols-[minmax(240px,38%)_1fr] border-b border-line last:border-b-0 hover:bg-bg2", active && "bg-bg3 hover:bg-bg3")}>
							<div className="flex min-w-0 items-center pl-3" style={{ paddingLeft: `${String(12 + Math.min(depth, MAX_INDENT) * 14)}px` }}>
								{children > 0 ? <Button
									variant="ghost"
									size="icon-xs"
									aria-label={`${collapsed.has(span.spanId) ? "Expand" : "Collapse"} ${span.name}`}
									aria-expanded={!collapsed.has(span.spanId)}
									onClick={() => { const next = new Set(collapsed); if (next.has(span.spanId)) next.delete(span.spanId); else next.add(span.spanId); setCollapsed(next); }}>{collapsed.has(span.spanId) ? <ChevronRightIcon /> : <ChevronDownIcon />}
								</Button> : <span className="w-6 shrink-0" />}
								<button
									type="button"
									onClick={(event) => { select(span.spanId, event.currentTarget); }}
									aria-pressed={active}
									className="min-w-0 flex-1 py-2 pr-3 text-left focus-visible:outline-2 focus-visible:outline-offset-[-2px]">
									<span className="flex items-center gap-1.5 text-sm text-fg3">{error && <CircleAlertIcon className="size-3 shrink-0 text-bad-fg" />}<span className="truncate">{span.serviceName || "Unknown service"}</span>{missingParent && <span title="Parent span is not available in these results" className="shrink-0 text-[10px] text-warn-fg">Missing parent</span>}
									</span>
									<span className={cn("mt-0.5 block truncate", error ? "text-bad-fg" : "text-foreground")} title={span.name}>{span.name || "Unnamed span"}
									</span>
								</button>
							</div>
							<button
								type="button"
								onClick={(event) => { select(span.spanId, event.currentTarget); }}
								aria-label={`${span.name}, ${durationLabel(span.durationNanos)}, starts at ${durationLabel(spanStart(span) - trace.start)}`}
								aria-pressed={active}
								className="relative mx-6 mt-0 mb-3 h-6 min-w-0 text-left sm:my-2 sm:h-auto focus-visible:outline-2 focus-visible:outline-offset-2">
								<span className="absolute inset-0 flex justify-between" aria-hidden>{TICKS.map((tick) => <span key={tick} className="h-full w-px bg-line" />)}
								</span>
								<span
									className={cn("absolute top-1/2 h-5 min-w-[3px] -translate-y-1/2 rounded-[3px] border", error ? "border-bad-fg/50 bg-bad-bg" : "border-info-fg/45 bg-info-bg", active && "ring-1 ring-foreground/30")}
									style={spanPosition(span, trace.start, trace.duration)} />
								<span className="relative z-10 flex h-full items-center justify-end pr-1 font-mono text-[10px] text-fg2">{durationLabel(span.durationNanos)}
								</span>
							</button>
						</div>;
					})}
				</div>
			</div>
			{incomplete && <p className="border-t border-line px-4 py-3 text-sm text-warn-fg">This waterfall is partial. Load more spans to include later operations.
			</p>}
		</div>
		{selected !== undefined && <SpanDetails key={selected.spanId} span={selected} start={trace.start} onClose={() => { select(null); }} />}
	</div>;
}

function SpanDetails({ span, start, onClose }: { span: TraceSpan; start: bigint; onClose: () => void; }) {
	const [search, setSearch] = useState("");
	const needle = search.toLowerCase();
	const error = spanError(span);
	const sections = [{ title: "Span attributes", values: span.spanAttributes }, { title: "Resource attributes", values: span.resourceAttributes }];
	return <aside ref={focusSpanDetails} tabIndex={-1} aria-label="Span details" className="min-w-0 border-t border-line xl:border-t-0 xl:border-l">
		<div className="flex items-center justify-between border-b border-line px-4 py-3">
			<h3 className="font-medium">Span details</h3>
			<Button variant="ghost" size="icon-xs" aria-label="Close span details" onClick={onClose}>
				<XIcon />
			</Button>
		</div>
		<div className="flex flex-col gap-5 p-4">
			<div>
				<p className="mb-1 text-sm text-fg3">{span.serviceName}</p>
				<h4 className="font-medium break-words">{span.name}</h4>
			</div>
			<dl className="grid grid-cols-2 gap-x-3 gap-y-3">
				<Detail label="Duration" value={durationLabel(span.durationNanos)} />
				<Detail label="Start offset" value={durationLabel(spanStart(span) - start)} />
				<Detail label="Kind" value={span.kind || "Unspecified"} />
				<Detail label="Status" value={error ? "Error" : span.statusCode || "Unset"} />
				<Detail label="Span ID" value={span.spanId} />
				<Detail label="Parent span ID" value={span.parentSpanId || "Root span"} />
			</dl>
			{span.statusMessage !== "" && <p className={cn("rounded-sm border border-line px-3 py-2 text-sm break-words", error && "border-bad-fg/30 bg-bad-bg text-bad-fg")}>{span.statusMessage}
			</p>}
			<div className="relative">
				<SearchInput
					aria-label="Search span attributes"
					placeholder="Find an attribute"
					value={search}
					className="pr-7"
					onChange={(event) => { setSearch(event.target.value); }} />{search !== "" && <Button
						variant="ghost"
						size="icon-xs"
						aria-label="Clear attribute search"
						className="absolute top-1 right-1"
						onClick={() => { setSearch(""); }}>
						<XIcon />
					</Button>}
			</div>
			{sections.map(({ title, values }) => {
				const entries = Object.entries(values).filter(([key, value]) => `${key} ${value}`.toLowerCase().includes(needle));
				return <section key={title}>
					<h4 className="mb-2 text-sm font-medium text-fg3">{title}</h4>{entries.length === 0 ? <p className="text-sm text-fg4">{search === "" ? "No attributes" : "No matching attributes"}</p> : <dl className="divide-y divide-line">{entries.map(([key, value]) => <div key={key} className="py-2 first:pt-0">
						<dt className="font-mono text-[11px] break-all text-fg3">{key}</dt>
						<dd className="mt-1 text-sm break-words whitespace-pre-wrap">{value}</dd>
					</div>)}
					</dl>}
				</section>;
			})}
			<section>
				<h4 className="mb-2 text-sm font-medium text-fg3">Instrumentation</h4>
				<p className="text-sm break-words">{span.scopeName || "Not provided"}{span.scopeVersion !== "" ? ` · ${span.scopeVersion}` : ""}</p>
			</section>
		</div>
	</aside>;
}

function Detail({ label, value }: { label: string; value: string; }) {
	return <div className="min-w-0">
		<dt className="text-sm text-fg3">{label}</dt>
		<dd className="mt-1 font-mono text-[11px] break-all">{value}</dd>
	</div>;
}

function focusSpanDetails(node: HTMLElement | null) {
	if (node === null) return;
	node.focus({ preventScroll: true });
	if (!window.matchMedia(DETAILS_SIDE_BY_SIDE).matches) node.scrollIntoView({ block: "start" });
}
