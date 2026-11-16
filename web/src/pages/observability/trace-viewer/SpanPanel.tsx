import type { SetTraceZoom } from "./useTraceZoom";
import { useMemo } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ChevronRightIcon, CircleAlertIcon, PlusIcon, MinusIcon } from "lucide-react";
import { parseAsString, useQueryStates } from "nuqs";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/design/Tabs";
import { cn } from "@/lib/utils";
import { useObs } from "../context";
import { queryKeyReason, quoteValue, withToken } from "../query";
import { SpanEvents } from "../SpanEvents";
import { SpanLinks } from "../SpanLinks";
import { zoomTo, failingSpan } from "../flame";
import { durationLabel, spanError, spanStart, type TraceSummary } from "../traces";
import { useTraceResources } from "./TraceColors";
import { spanAttributes, ATTRIBUTE_TABS } from "./attributes";

export function SpanPanel({
	trace,
	selected,
	missing,
	select,
	label,
	onZoom,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	missing: boolean;
	select: (span: TraceSpan) => void;
	label: (span: TraceSpan) => string;
	onZoom: SetTraceZoom;
}) {
	const { color, selfTime } = useTraceResources();
	const { tokens, text, setQuery } = useObs();
	const [, setOpen] = useQueryStates({
		trace: parseAsString,
		span: parseAsString,
	});
	// Filtering closes the drawer so the trace list shows the result.
	const filter = (neg: boolean, key: string, value: string) => {
		setQuery(withToken(tokens, { neg, key, value: quoteValue(value) }), text);
		void setOpen({ trace: null, span: null });
	};
	const reason = (key: string) =>
		key in selected.spanAttributes || key in selected.resourceAttributes
			? queryKeyReason(key, "traces")
			: `${key} isn't an attribute`;
	const cause = useMemo(() => failingSpan(trace, selected), [trace, selected]);
	const tabs = spanAttributes(selected);
	const facts: [string, string][] = [
		["duration", durationLabel(selected.durationNanos)],
		["self time", durationLabel(selfTime(selected))],
		["start offset", durationLabel(spanStart(selected) - trace.start)],
		["kind", selected.kind],
		["status", selected.statusCode],
		["parent", selected.parentSpanId || "root span"],
	];
	return (
		<aside className="flex min-w-0 flex-col gap-4 border-line min-[1100px]:border-l min-[1100px]:pl-5">
			{missing && (
				<p role="status" className="m-0 text-[12px] text-fg3">
					The selected span is unavailable.
				</p>
			)}
			<section aria-label="Selected span" className="flex min-w-0 flex-col gap-1.5">
				<div className="flex min-w-0 items-center gap-2">
					<i className="size-2 shrink-0 rounded-full" style={{ background: color(selected) }} />
					<h3 className="m-0 min-w-0 break-words font-mono text-[14px] [overflow-wrap:anywhere]">
						{selected.name === "" ? "Unnamed span" : selected.name}
					</h3>
					<Badge tone={spanError(selected) ? "bad" : "muted"}>{spanError(selected) ? "Error" : "OK"}</Badge>
				</div>
				<span className="text-[12px] text-fg3">{label(selected)}</span>
				<Button
					variant="outline"
					size="sm"
					onClick={() => {
						onZoom(zoomTo(trace, selected));
					}}
				>
					Zoom to span
				</Button>
			</section>
			<section aria-label="Span details" className="rounded-lg border border-line bg-background p-3.5">
				<h4 className="m-0 mb-2 text-[12px] font-medium text-fg2">Details</h4>
				<dl className="m-0 grid grid-cols-2 gap-x-3 gap-y-2 text-[12px]">
					{facts.map(([key, value]) => (
						<div key={key} className="min-w-0">
							<dt className="text-fg3">{key}</dt>
							<dd className="m-0 font-mono [overflow-wrap:anywhere]">{value === "" ? "—" : value}</dd>
						</div>
					))}
					<div className="col-span-2 min-w-0">
						<dt className="text-fg3">span id</dt>
						<dd className="m-0">
							<CopyId value={selected.spanId} title="Copy span ID" className="max-w-full px-0 font-mono text-[12px]">
								<span className="truncate">{selected.spanId}</span>
							</CopyId>
						</dd>
					</div>
				</dl>
			</section>
			{cause !== undefined && (
				<button
					type="button"
					onClick={() => {
						select(cause);
					}}
					className="flex items-center gap-2 rounded-lg border border-bad-fg/25 bg-bad-bg px-3 py-2 text-left text-[12px] hover:brightness-[0.98] focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
				>
					<CircleAlertIcon className="size-4 shrink-0 text-bad-fg" />
					Failed in{" "}
					<span className="min-w-0 break-words font-mono">{cause.name === "" ? "Unnamed span" : cause.name}</span>
					<ChevronRightIcon className="ml-auto size-4 shrink-0 text-fg3" />
				</button>
			)}
			{selected.statusMessage !== "" && (
				<pre
					className={cn(
						"m-0 rounded-lg border border-line px-3 py-2.5 font-mono text-[12px] break-words whitespace-pre-wrap",
						spanError(selected) ? "border-bad-fg/25 bg-bad-bg text-bad-fg" : "bg-panel",
					)}
				>
					{selected.statusMessage}
				</pre>
			)}
			<SpanEvents span={selected} />
			<SpanLinks span={selected} />
			<section aria-label="Span attributes" className="min-w-0">
				<Tabs defaultValue="Attributes" key={selected.spanId}>
					<TabsList className="overflow-x-auto">
						{ATTRIBUTE_TABS.map((tab) => (
							<TabsTrigger key={tab} value={tab} className="tv-stab px-2 text-[12px]">
								{tab}
							</TabsTrigger>
						))}
					</TabsList>
					{ATTRIBUTE_TABS.map((tab) => (
						<TabsContent key={tab} value={tab} className="pt-2">
							{tabs[tab].length === 0 ? (
								<p className="py-3 text-[12px] text-fg3">No {tab.toLowerCase()} attributes recorded.</p>
							) : (
								<dl className="m-0">
									{tabs[tab].map(([key, value]) => {
										const why = reason(key);
										return (
											<div
												key={key}
												className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-start gap-2 border-b border-line py-2 text-[11.5px] last:border-0"
											>
												<dt className="min-w-0 truncate font-mono text-fg3" title={key}>
													{key}
												</dt>
												<dd className="m-0 min-w-0 font-mono [overflow-wrap:anywhere]">{value}</dd>
												<dd className="m-0 flex items-center gap-0.5">
													<Button
														variant="ghost"
														size="icon-sm"
														disabled={why !== null}
														title={why ?? "Only traces with this value"}
														aria-label={`Only traces with this value: ${key}`}
														className="size-5"
														onClick={() => {
															filter(false, key, value);
														}}
													>
														<PlusIcon className="size-3" />
													</Button>
													<Button
														variant="ghost"
														size="icon-sm"
														disabled={why !== null}
														title={why ?? "Hide traces with this value"}
														aria-label={`Hide traces with this value: ${key}`}
														className="size-5"
														onClick={() => {
															filter(true, key, value);
														}}
													>
														<MinusIcon className="size-3" />
													</Button>
												</dd>
											</div>
										);
									})}
								</dl>
							)}
						</TabsContent>
					))}
				</Tabs>
			</section>
		</aside>
	);
}
