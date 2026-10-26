import { useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { CheckIcon, ChevronDownIcon, ChevronRightIcon, CircleAlertIcon, CopyIcon, XIcon } from "lucide-react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

import { ColumnResizeHandle } from "@/components/design/ColumnResizeHandle";
import { useColumnWidths } from "@/hooks/useColumnWidths";
import { Input } from "@/components/design/Input";
import { Button } from "@/components/design/Button";
import { Sheet, SheetContent, SheetTitle } from "@/components/design/Sheet";
import { TraceDot, TraceTabs, TraceTab, TraceTabList, TraceTabPanel } from "@/components/design/Trace";
import { useIsMobile } from "@/hooks/use-mobile";
import { useCopy } from "@/hooks/useCopy";
import { cn } from "@/lib/utils";

import { useObs } from "./context";
import { traceServiceColor, traceServiceLabel } from "./trace-service";
import { axisLabel, durationLabel, spanError, spanPosition, spanStart, spanTree, type TraceSummary } from "./traces";

const TICKS = [0, 25, 50, 75, 100];
const MAX_INDENT = 8;
const WATERFALL_LAYOUT = {
	minimums: [180, 160, 64],
	initial: (width: number) => [width * 0.42, width * 0.58 - 70, 70],
};
const NARROW_WATERFALL_LAYOUT = {
	minimums: [128, 112, 56],
	initial: (width: number) => [width * 0.52, width * 0.48 - 58, 58],
};

export function TraceWaterfall({ trace, incomplete }: { trace: TraceSummary; incomplete: boolean }) {
	const [params, setParams] = useSearchParams();
	const { resourceById } = useObs();
	const mobile = useIsMobile();
	const {
		ref: columnRef,
		style: columnStyle,
		handles,
		resize,
	} = useColumnWidths(
		"loco:trace-waterfall-columns:v1",
		mobile ? "narrow" : "wide",
		mobile ? NARROW_WATERFALL_LAYOUT : WATERFALL_LAYOUT,
	);
	const nameHandle = handles[0];
	const timelineHandle = handles[1];
	const [collapsed, setCollapsed] = useState(() => new Set<string>());
	const requestedSpan = params.get("span");
	const selected = trace.spans.find((span) => span.spanId === requestedSpan);
	const rows = spanTree(trace.spans, collapsed);
	const triggerRef = useRef<HTMLButtonElement | null>(null);
	const select = (id: string | null, target?: HTMLButtonElement) => {
		if (target !== undefined) triggerRef.current = target;
		if (id === null) triggerRef.current?.focus();
		const next = new URLSearchParams(params);
		if (id === null) next.delete("span");
		else next.set("span", id);
		setParams(next, { replace: true });
	};
	const services = [...new Map(trace.spans.map((span) => [span.serviceName, span])).values()];
	const label = (span: TraceSpan) => traceServiceLabel(span, resourceById.get(span.resourceId)?.name);
	return (
		<>
			<div className="trace-toolbar">
				<h2 className="font-medium">
					Waterfall{" "}
					<span className="ml-2 font-normal text-fg3">
						{trace.spans.length} loaded {trace.spans.length === 1 ? "span" : "spans"}
					</span>
				</h2>
				<Button
					variant="ghost"
					size="xs"
					disabled={collapsed.size === 0}
					onClick={() => {
						setCollapsed(new Set());
					}}
				>
					Expand all
				</Button>
			</div>
			{requestedSpan !== null && selected === undefined && (
				<div
					role="status"
					className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-2.5 py-2 text-xs text-fg3"
				>
					<span>
						{incomplete
							? "The selected span has not loaded. Load more spans to find it."
							: "The selected span is unavailable in this time range."}
					</span>
					<Button
						variant="ghost"
						size="xs"
						onClick={() => {
							select(null);
						}}
					>
						Clear span selection
					</Button>
				</div>
			)}
			<div className="trace-span-rows" ref={columnRef} style={columnStyle}>
				<div className="trace-ruler">
					<span className="trace-ruler-label">
						Service / operation
						{nameHandle !== undefined && (
							<ColumnResizeHandle
								key={mobile ? "narrow-name" : "wide-name"}
								label="Resize waterfall name column"
								{...nameHandle}
								onResize={(value, commit) => {
									resize(0, value, commit);
								}}
							/>
						)}
					</span>
					<div className="trace-ruler-time">
						<div className="trace-axis">
							{TICKS.map((tick) => (
								<span key={tick} className="trace-tick font-mono" style={{ left: `${String(tick)}%` }}>
									{axisLabel((trace.duration * BigInt(tick)) / 100n, trace.duration)}
								</span>
							))}
						</div>
						{timelineHandle !== undefined && (
							<ColumnResizeHandle
								key={mobile ? "narrow-timeline" : "wide-timeline"}
								label="Resize waterfall timeline column"
								{...timelineHandle}
								onResize={(value, commit) => {
									resize(1, value, commit);
								}}
							/>
						)}
					</div>
					<span className="trace-duration">Duration</span>
				</div>
				{rows.map(({ span, depth, children, missingParent }) => {
					const error = spanError(span);
					const active = selected?.spanId === span.spanId;
					const color = traceServiceColor(span.serviceName);
					return (
						<div key={span.spanId} className="trace-span-row" data-selected={active}>
							<div
								className="trace-span-name"
								style={{
									paddingLeft: `${String(5 + Math.min(depth, MAX_INDENT) * (mobile ? 8 : 12))}px`,
								}}
							>
								{children > 0 ? (
									<button
										type="button"
										className="flex size-4 shrink-0 items-center justify-center rounded-sm focus-visible:outline-2"
										aria-label={`${collapsed.has(span.spanId) ? "Expand" : "Collapse"} ${span.name}`}
										aria-expanded={!collapsed.has(span.spanId)}
										onClick={() => {
											const next = new Set(collapsed);
											if (next.has(span.spanId)) next.delete(span.spanId);
											else next.add(span.spanId);
											setCollapsed(next);
										}}
									>
										{collapsed.has(span.spanId) ? (
											<ChevronRightIcon className="size-3" />
										) : (
											<ChevronDownIcon className="size-3" />
										)}
									</button>
								) : (
									<span className="w-4 shrink-0" />
								)}
								<button
									type="button"
									className="trace-span-label"
									aria-label={`${label(span)} ${span.name}`}
									onClick={(event) => {
										select(span.spanId, event.currentTarget);
									}}
									aria-pressed={active}
									title={`${span.serviceName} · ${span.name}${missingParent ? " · Parent span unavailable" : ""}`}
								>
									<TraceDot color={color} />
									<span className="trace-service-label">{label(span)}</span>
									<span className="trace-operation">{span.name || "Unnamed span"}</span>
									{error && <CircleAlertIcon aria-label="Error span" className="size-3 shrink-0 text-bad-fg" />}
									{missingParent && (
										<span aria-label="Parent span unavailable" className="text-warn-fg">
											↳
										</span>
									)}
								</button>
							</div>
							<button
								type="button"
								className="trace-span-time"
								onClick={(event) => {
									select(span.spanId, event.currentTarget);
								}}
								aria-label={`${span.name}, ${durationLabel(span.durationNanos)}, starts at ${durationLabel(spanStart(span) - trace.start)}`}
								aria-pressed={active}
							>
								{TICKS.map((tick) => (
									<span key={tick} className="trace-grid-line" style={{ left: `${String(tick)}%` }} aria-hidden />
								))}
								<span
									className="trace-span-bar"
									style={{
										...spanPosition(span, trace.start, trace.duration),
										background: color,
									}}
								/>
							</button>
							<span className="trace-duration">{durationLabel(span.durationNanos)}</span>
						</div>
					);
				})}
			</div>
			<div className="trace-legend">
				{services.map((span) => (
					<span key={span.serviceName} className="inline-flex items-center gap-1.5" title={span.serviceName}>
						<TraceDot color={traceServiceColor(span.serviceName)} />
						{label(span)}
					</span>
				))}
			</div>
			{mobile ? (
				<Sheet
					open={selected !== undefined}
					onOpenChange={(open) => {
						if (!open) select(null);
					}}
				>
					<SheetContent
						side="bottom"
						showCloseButton={false}
						finalFocus={triggerRef}
						className="max-h-[70dvh] overflow-y-auto rounded-t-lg"
					>
						<SheetTitle className="sr-only">Span details</SheetTitle>
						{selected !== undefined && (
							<SpanDetails
								key={selected.spanId}
								span={selected}
								label={label(selected)}
								start={trace.start}
								onClose={() => {
									select(null);
								}}
							/>
						)}
					</SheetContent>
				</Sheet>
			) : (
				selected !== undefined && (
					<SpanDetails
						key={selected.spanId}
						span={selected}
						label={label(selected)}
						start={trace.start}
						onClose={() => {
							select(null);
						}}
					/>
				)
			)}
		</>
	);
}

function SpanDetails({
	span,
	label,
	start,
	onClose,
}: {
	span: TraceSpan;
	label: string;
	start: bigint;
	onClose: () => void;
}) {
	const [search, setSearch] = useState("");
	const [copied, copy] = useCopy();
	const error = spanError(span);
	const sections = [
		{ key: "span", title: "Attributes", values: span.spanAttributes },
		{ key: "resource", title: "Resource", values: span.resourceAttributes },
		{
			key: "scope",
			title: "Instrumentation",
			values: {
				name: span.scopeName || "Not provided",
				version: span.scopeVersion || "Not provided",
			},
		},
	];
	return (
		<aside tabIndex={-1} aria-label="Span details" className="trace-inspector">
			<div className="trace-inspector-head">
				<h3 className="trace-inspector-title">
					<TraceDot color={traceServiceColor(span.serviceName)} />
					<span className="max-w-40 truncate text-fg3" title={span.serviceName}>
						{label}
					</span>
					<span className="truncate" title={span.name}>
						{span.name}
					</span>
					{error && <span className="shrink-0 rounded-sm bg-bad-bg px-1.5 py-0.5 text-[10px] text-bad-fg">Error</span>}
				</h3>
				<Button variant="ghost" size="icon-xs" aria-label="Close span details" onClick={onClose}>
					<XIcon />
				</Button>
			</div>
			<dl className="trace-inspector-meta">
				<Detail label="Duration" value={durationLabel(span.durationNanos)} />
				<Detail label="Offset" value={durationLabel(spanStart(span) - start)} />
				<Detail label="Kind" value={span.kind || "Unspecified"} />
				<Detail label="Status" value={span.statusCode || "Unset"} />
				<div>
					<dt>Span ID</dt>
					<dd>
						<button
							type="button"
							className="inline-flex items-center gap-1 hover:text-foreground focus-visible:outline-2"
							aria-label="Copy span ID"
							onClick={() => {
								copy("span", span.spanId);
							}}
						>
							<code>{span.spanId}</code>
							{copied === "span" ? <CheckIcon className="size-2.5" /> : <CopyIcon className="size-2.5" />}
						</button>
					</dd>
				</div>
				<Detail label="Parent" value={span.parentSpanId || "Root span"} />
			</dl>
			<TraceTabs defaultValue="span">
				<div className="flex flex-wrap items-center justify-between border-b border-line">
					<TraceTabList aria-label="Span information" className="border-b-0">
						{sections.map((section) => (
							<TraceTab key={section.key} value={section.key}>
								{section.title}
							</TraceTab>
						))}
					</TraceTabList>
					<div className="relative mr-2 max-sm:mb-1 max-sm:ml-2 max-sm:w-full">
						<Input
							aria-label="Search span attributes"
							placeholder="Filter attributes…"
							value={search}
							onChange={(event) => {
								setSearch(event.target.value);
							}}
							className="h-6 w-40 rounded-sm pr-6 text-[10px] max-sm:w-full"
						/>
						{search !== "" && (
							<Button
								variant="ghost"
								size="icon-xs"
								aria-label="Clear attribute search"
								className="absolute top-0 right-0 size-6"
								onClick={() => {
									setSearch("");
									document.querySelector<HTMLInputElement>('input[aria-label="Search span attributes"]')?.focus();
								}}
							>
								<XIcon />
							</Button>
						)}
					</div>
				</div>
				{span.statusMessage !== "" && (
					<p
						className={cn(
							"border-b border-line px-2.5 py-1.5 text-[11px] break-words",
							error && "bg-bad-bg text-bad-fg",
						)}
					>
						{span.statusMessage}
					</p>
				)}
				{sections.map(({ key, title, values }) => {
					const entries = Object.entries(values).filter(([name, value]) =>
						`${name} ${value}`.toLowerCase().includes(search.toLowerCase()),
					);
					return (
						<TraceTabPanel key={key} value={key} className="trace-attributes" aria-label={title}>
							<dl>
								{entries.map(([name, value]) => (
									<div key={name} className="trace-attribute-row">
										<dt>{name}</dt>
										<dd>{value}</dd>
									</div>
								))}
							</dl>
							{entries.length === 0 && (
								<p className="py-3 text-xs text-fg3">{search === "" ? "No attributes" : "No matching attributes"}</p>
							)}
						</TraceTabPanel>
					);
				})}
			</TraceTabs>
		</aside>
	);
}

function Detail({ label, value }: { label: string; value: string }) {
	return (
		<div>
			<dt>{label}</dt>
			<dd className="font-mono break-all">{value}</dd>
		</div>
	);
}
