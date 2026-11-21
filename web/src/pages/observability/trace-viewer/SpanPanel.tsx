import { useMemo, type ReactNode } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ChevronRightIcon, CircleAlertIcon, PlusIcon, MinusIcon } from "lucide-react";
import { parseAsString, useQueryStates } from "nuqs";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { CopyButton } from "@/components/design/CopyButton";
import { CopyId } from "@/components/design/CopyId";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/design/Tabs";
import { formatClock } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { cn } from "@/lib/utils";
import { useObs } from "../context";
import { queryKeyReason, quoteValue, withToken } from "../query";
import { SpanLinks } from "../SpanLinks";
import { failingSpan } from "../flame";
import { durationLabel, spanError, spanStart, spanStatus, type TraceSummary } from "../traces";
import { useTraceResources } from "./TraceColors";
import { attributeGroups, spanAttributes, ATTRIBUTE_TABS } from "./attributes";

export function SpanPanel({
	trace,
	selected,
	missing,
	select,
	label,
	tab: activeTab,
	onTab,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	missing: boolean;
	select: (span: TraceSpan) => void;
	label: (span: TraceSpan) => string;
	tab: string;
	onTab: (tab: string) => void;
}) {
	const timePref = useTimePreferences();
	const { color, selfTime } = useTraceResources();
	const { tokens, text, setQuery } = useObs();
	const [, setOpen] = useQueryStates({ trace: parseAsString, span: parseAsString });
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
	const stack = selected.events.find((event) => event.attributes["exception.stacktrace"])?.attributes[
		"exception.stacktrace"
	];
	const tabNames = [
		...ATTRIBUTE_TABS.slice(0, 1),
		...(selected.links.length > 0 ? ["Links"] : []),
		...ATTRIBUTE_TABS.slice(1),
	];
	const parent = trace.spans.find((span) => span.spanId === selected.parentSpanId);
	const share =
		trace.duration === 0n ? 100 : Math.round((Number(selected.durationNanos) / Number(trace.duration)) * 100);
	const facts: { label: string; value: ReactNode; sub?: string; mono?: boolean }[] = [
		{ label: "Resource", value: label(selected), mono: false },
		{
			label: "Duration",
			value: durationLabel(selected.durationNanos),
			sub: `${share.toString()}% of trace`,
		},
		{ label: "Self time", value: durationLabel(selfTime(selected)) },
		{
			label: "Starts at",
			value: `+${durationLabel(spanStart(selected) - trace.start)}`,
			sub: formatClock(Number(spanStart(selected) / 1_000_000n), true, timePref),
		},
		{ label: "Kind", value: selected.kind },
		{ label: "Status", value: spanStatus(selected) },
		{
			label: "Span ID",
			value: (
				<CopyId value={selected.spanId} title="Copy span ID" className="max-w-full px-0 font-mono text-[12.5px]">
					<span className="truncate">{selected.spanId}</span>
				</CopyId>
			),
		},
		{
			label: "Parent",
			value: parent === undefined ? selected.parentSpanId : parent.name || "Unnamed span",
		},
	];
	return (
		<aside className="flex min-w-0 flex-col gap-4">
			{missing && (
				<p role="status" className="m-0 text-[12px] text-fg3">
					The selected span is unavailable.
				</p>
			)}
			{selected.spanId !== trace.root.spanId && (
				<section aria-label="Selected span" className="flex min-w-0 items-center gap-2">
					<i className="size-2 shrink-0 rounded-full" style={{ background: color(selected) }} />
					<h3 className="m-0 min-w-0 break-words font-mono text-[14px] [overflow-wrap:anywhere]">
						{selected.name === "" ? "Unnamed span" : selected.name}
					</h3>
					<Badge tone={spanError(selected) ? "bad" : "muted"}>{spanStatus(selected)}</Badge>
				</section>
			)}
			<section aria-label="Span details" className="flex min-w-0 flex-col gap-2">
				<h4 className="m-0 text-[12px] font-medium text-fg2">Details</h4>
				<dl className="m-0 grid grid-cols-2 gap-x-4 gap-y-3 rounded-lg border border-line bg-background px-3.5 py-3">
					{facts.map((fact) => (
						<div key={fact.label} className="flex min-w-0 flex-col gap-[3px]">
							<dt className="text-[11.5px] text-fg3">{fact.label}</dt>
							<dd
								className={cn(
									"m-0 text-[13px] [overflow-wrap:anywhere]",
									fact.mono !== false && "font-mono text-[12.5px]",
								)}
							>
								{fact.value === "" ? "—" : fact.value}
							</dd>
							{fact.sub !== undefined && <dd className="m-0 font-mono text-[11.5px] text-fg3">{fact.sub}</dd>}
						</div>
					))}
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
			{stack !== undefined && (
				<div className="relative min-w-0 rounded-lg border border-bad-fg/25 bg-bad-bg">
					<div className="absolute top-1.5 right-1.5">
						<CopyButton text={stack} name="Copy stack trace" toastText="Copied stack trace" />
					</div>
					<pre className="m-0 max-h-[360px] overflow-auto px-3 py-2.5 pr-24 font-mono text-[12px] whitespace-pre-wrap text-bad-fg [overflow-wrap:anywhere]">
						{stack}
					</pre>
				</div>
			)}
			<section aria-label="Span attributes" className="min-w-0">
				<Tabs
					value={activeTab}
					onValueChange={(value) => {
						onTab(String(value));
					}}
				>
					<TabsList aria-label="Span detail tabs" variant="segmented" className="max-w-full overflow-x-auto">
						{tabNames.map((tab) => (
							<TabsTrigger key={tab} value={tab} className="tv-stab px-2 text-[12px]">
								{tab}
							</TabsTrigger>
						))}
					</TabsList>
					<TabsContent value="Links" className="pt-2">
						<SpanLinks span={selected} />
					</TabsContent>
					{ATTRIBUTE_TABS.map((tab) => (
						<TabsContent key={tab} value={tab} className="pt-2">
							{tabs[tab].length === 0 ? (
								<p className="py-3 text-[12px] text-fg3">No {tab.toLowerCase()} attributes recorded.</p>
							) : (
								<div className="flex flex-col gap-3.5">
									{(tab === "Attributes" ? attributeGroups(tabs[tab]) : [["", tabs[tab]] as const]).map(
										([title, rows]) => (
											<div key={title} className="flex min-w-0 flex-col">
												{title !== "" && <h5 className="m-0 pb-1 text-[12.5px] font-normal text-fg3">{title}</h5>}
												<dl className="m-0">
													{rows.map(([key, value]) => {
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
											</div>
										),
									)}
								</div>
							)}
						</TabsContent>
					))}
				</Tabs>
			</section>
		</aside>
	);
}
