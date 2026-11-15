import { useTimePreferences } from "@/lib/use-time-preferences";
import { useRef, useState } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ChevronDownIcon, ChevronUpIcon } from "lucide-react";
import { Button } from "@/components/design/Button";
import { Spinner } from "@/components/design/Spinner";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/design/Tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage } from "@/lib/error-handler";
import { formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";
import { useObs } from "../context";
import { levelStyle } from "../format";
import { LogDetail } from "../LogDetail";
import { DEFAULT_COLUMNS } from "../columns";
import type { LogRow } from "../rows";
import { durationLabel, type TraceSummary } from "../traces";
import { Infrastructure } from "./Infrastructure";
import type { useTraceLogs } from "./trace-logs";
import { descendantIds } from "../flame";

export function TraceLogPanel({
	trace,
	selected,
	logs,
	highlighted,
	onHover,
	opened,
	onOpen,
	tab,
	onTab,
	collapsed,
	onCollapsed,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	logs: ReturnType<typeof useTraceLogs>;
	highlighted: Set<string>;
	onHover: (row: LogRow | null) => void;
	opened: LogRow | null;
	onOpen: (row: LogRow | null) => void;
	tab: string;
	onTab: (tab: string) => void;
	collapsed: boolean;
	onCollapsed: (collapsed: boolean) => void;
}) {
	const timePref = useTimePreferences();
	const { goTo } = useObs();
	const [columns, setColumns] = useState(DEFAULT_COLUMNS);
	const [scope, setScope] = useState("trace");
	const [height, setHeight] = useState(240);
	const resizeRef = useRef<{ y: number; height: number } | null>(null);
	const descendants = descendantIds(trace, selected.spanId);
	const rows = scope === "span" ? logs.rows.filter((r) => descendants.has(r.entry.spanId)) : logs.rows;
	const more = logs.newerCursor !== "";
	const openLogs = () => {
		goTo("logs", {
			range: { kind: "absolute", from: logs.from, to: logs.to },
			tokens: [{ key: "trace", value: trace.id, neg: false }],
		});
	};
	const clamp = (n: number) => Math.max(120, Math.min(620, n));
	return (
		<section
			aria-label="Trace bottom panel"
			className="tv-bottom relative flex min-h-0 shrink-0 min-[1100px]:max-h-[65%] flex-col border-t border-line bg-background"
			style={{ height: collapsed ? "auto" : height }}
		>
			{!collapsed && (
				<div
					role="separator"
					aria-label="Resize bottom panel"
					aria-orientation="horizontal"
					aria-valuenow={height}
					aria-valuemin={120}
					aria-valuemax={620}
					tabIndex={0}
					className="absolute inset-x-0 -top-1 z-10 h-2 cursor-row-resize touch-none focus-visible:ring-2 focus-visible:ring-ring"
					onPointerDown={(e) => {
						e.currentTarget.setPointerCapture(e.pointerId);
						resizeRef.current = { y: e.clientY, height };
					}}
					onPointerMove={(e) => {
						if (resizeRef.current !== null)
							setHeight(clamp(resizeRef.current.height + resizeRef.current.y - e.clientY));
					}}
					onLostPointerCapture={() => {
						resizeRef.current = null;
					}}
					onKeyDown={(e) => {
						if (e.key === "ArrowUp" || e.key === "ArrowDown") {
							e.preventDefault();
							setHeight(clamp(height + (e.key === "ArrowUp" ? 24 : -24)));
						}
					}}
				/>
			)}
			<Tabs
				value={tab}
				onValueChange={(value) => {
					onTab(String(value));
				}}
				className="flex min-h-0 flex-1 flex-col"
			>
				<div className="flex flex-wrap items-center gap-2 border-b border-line px-2">
					<TabsList className="w-auto border-0">
						<TabsTrigger
							value="logs"
							onClick={() => {
								onCollapsed(false);
							}}
						>
							Logs ({logs.rows.length}
							{more ? "+" : ""})
						</TabsTrigger>
						<TabsTrigger
							value="infra"
							onClick={() => {
								onCollapsed(false);
							}}
						>
							Infrastructure
						</TabsTrigger>
					</TabsList>
					{tab === "logs" && !collapsed && (
						<>
							<ToggleGroup
								variant="segmented"
								aria-label="Which logs"
								value={[scope]}
								onValueChange={(v) => {
									if (v[0]) setScope(v[0]);
									onOpen(null);
								}}
							>
								<ToggleGroupItem value="trace" className="h-6 text-[11px]">
									Whole trace
								</ToggleGroupItem>
								<ToggleGroupItem value="span" className="h-6 text-[11px]">
									This span
								</ToggleGroupItem>
							</ToggleGroup>
							<Button variant="link" size="sm" className="h-6 text-[11px]" onClick={openLogs}>
								Open in Logs
							</Button>
						</>
					)}
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={collapsed ? "Expand bottom panel" : "Collapse bottom panel"}
						className="ml-auto"
						onClick={() => {
							onCollapsed(!collapsed);
						}}
					>
						{collapsed ? <ChevronUpIcon /> : <ChevronDownIcon />}
					</Button>
				</div>
				{!collapsed && (
					<>
						<TabsContent value="logs" className="min-h-0 flex-1 overflow-auto">
							{opened !== null ? (
								<LogDetail
									row={opened}
									onFilterToken={(token) => {
										goTo("logs", {
											range: { kind: "absolute", from: logs.from, to: logs.to },
											tokens: token.key === "trace" ? [token] : [{ key: "trace", value: trace.id, neg: false }, token],
											focusTs: opened.ts,
										});
									}}
									words={[]}
									columns={columns}
									onColumns={setColumns}
									onPrev={null}
									onNext={null}
									onClose={() => {
										onOpen(null);
									}}
								/>
							) : (
								<section aria-label="Logs in this trace" className="min-w-0">
									{logs.isLoading && (
										<p role="status" className="flex items-center gap-2 px-3 text-fg3">
											<Spinner />
											Loading logs…
										</p>
									)}
									{logs.error !== null && (
										<p role="alert" className="px-3 text-bad-fg">
											{getErrorMessage(logs.error, "Couldn’t load trace logs")}
										</p>
									)}
									{!logs.isLoading && logs.error === null && rows.length === 0 && (
										<p className="px-3 py-4 text-[12px] text-fg3">
											{more
												? "No matching lines among the loaded logs. Open in Logs to search the full trace."
												: scope === "span"
													? "No logs from this span or its children."
													: "No log lines carry this trace ID."}
										</p>
									)}
									<ol className="m-0 list-none p-0">
										{rows.map((row) => (
											<li key={row.key}>
												<button
													type="button"
													aria-label={`Open log: ${row.entry.body}`}
													data-log-key={row.key}
													onMouseEnter={() => {
														onHover(row);
													}}
													onMouseLeave={() => {
														onHover(null);
													}}
													onFocus={() => {
														onHover(row);
													}}
													onBlur={() => {
														onHover(null);
													}}
													onClick={() => {
														onOpen(row);
													}}
													className={cn(
														"grid w-full grid-cols-[minmax(0,1fr)_80px_44px] sm:grid-cols-[minmax(90px,128px)_66px_44px_minmax(0,1fr)] items-baseline gap-2 border-b border-line px-3 py-1.5 text-left font-mono text-[11px] hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
														highlighted.has(row.key) && "bg-info/10 shadow-[inset_3px_0_0_var(--info)]",
													)}
												>
													<span className="text-fg3">{formatClock(row.ts, true, timePref)}</span>
													<span className="text-fg3">+{durationLabel(BigInt(row.ts) * 1_000_000n - trace.start)}</span>
													<span className={levelStyle(row.level).body}>{row.label}</span>
													<span className="col-span-3 min-w-0 sm:col-span-1 [overflow-wrap:anywhere]">
														<span className="mr-2 inline-flex items-center gap-1 text-fg3">
															<i className="size-1.5 rounded-full" style={{ background: row.color }} />
															{row.resourceName}
														</span>
														{row.entry.body}
													</span>
												</button>
											</li>
										))}
									</ol>
									{more && (
										<Button variant="link" size="sm" onClick={openLogs}>
											All lines in Logs
										</Button>
									)}
								</section>
							)}
						</TabsContent>
						<TabsContent value="infra" className="min-h-0 flex-1 overflow-auto">
							<Infrastructure key={selected.spanId} trace={trace} selected={selected} />
						</TabsContent>
					</>
				)}
			</Tabs>
		</section>
	);
}
