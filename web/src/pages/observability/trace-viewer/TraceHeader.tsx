import { useTimePreferences } from "@/lib/use-time-preferences";
import {
	CheckIcon,
	CopyIcon,
	DownloadIcon,
	EllipsisIcon,
	LinkIcon,
	ScrollTextIcon,
	XIcon,
	BracesIcon,
	BoxIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { Badge } from "@/components/design/Badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { Button } from "@/components/design/Button";
import { MetaLine } from "@/components/design/MetaLine";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { SheetTitle, SheetDescription } from "@/components/design/Sheet";
import { useCopy } from "@/hooks/useCopy";
import { agoLabel, formatClock, formatFullTime, formatMonthDay, timeZoneLabel } from "@/lib/time";
import { aroundWindow } from "@/lib/obs-time";
import { resourcePath } from "@/lib/routes";
import { cn } from "@/lib/utils";
import { useObs } from "../context";
import { useTraceResources } from "./TraceColors";
import { durationLabel, spanError, type TraceSummary } from "../traces";
import { traceIdentity, traceResourceKey, traceServiceLabel } from "../trace-service";
import { traceJson } from "./trace-json";

export function TraceHeader({
	trace,
	traceId,
	arriving,
	onClose,
	highlight,
	pinned,
	onHover,
	onPin,
	onLinks,
}: {
	trace: TraceSummary | undefined;
	traceId: string;
	arriving: boolean;
	onClose: () => void;
	highlight: string | null;
	pinned: string | null;
	onHover: (service: string | null) => void;
	onPin: (service: string | null) => void;
	onLinks: () => void;
}) {
	const timePref = useTimePreferences();
	const { orgId, workspaceId, resourceById, goTo } = useObs();
	const [copied, copy] = useCopy();
	const [now] = useState(Date.now);
	const [expanded, setExpanded] = useState(false);
	const { shares, color } = useTraceResources();
	const items =
		trace === undefined
			? []
			: shares.map((share) => {
					const spans = trace.spans.filter((span) => traceResourceKey(span) === share.key);
					const first = spans[0];
					if (first === undefined) throw new Error("Trace resource has no spans");
					const slug = resourceById.get(first.resourceId)?.slug;
					return {
						...share,
						...traceIdentity(first, slug),
						label: traceServiceLabel(first, slug),
						color: color(first),
						errors: spans.filter(spanError).length,
					};
				});
	const top = items.slice(0, 4);
	const rest = items.slice(4);
	const restShare = rest.reduce((sum, item) => sum + item.share, 0);
	const percent = (share: number) => (share < 0.01 ? "<1%" : `${Math.round(share * 100).toString()}%`);
	const interactions = (key: string) => ({
		onMouseEnter: () => {
			onHover(key);
		},
		onMouseLeave: () => {
			onHover(null);
		},
		onFocus: () => {
			onHover(key);
		},
		onBlur: () => {
			onHover(null);
		},
		onClick: () => {
			onPin(pinned === key ? null : key);
		},
	});
	const resource = trace === undefined ? undefined : resourceById.get(trace.root.resourceId);
	const rootIdentity = trace === undefined ? undefined : traceIdentity(trace.root, resource?.slug);
	const start = trace === undefined ? 0 : Number(trace.start / 1_000_000n);
	const linkCount = trace === undefined ? 0 : trace.spans.reduce((n, span) => n + span.links.length, 0);
	const download = () => {
		if (trace === undefined) return;
		const url = URL.createObjectURL(new Blob([traceJson(trace.spans)], { type: "application/json" }));
		const a = document.createElement("a");
		a.href = url;
		a.download = `trace-${traceId}.json`;
		a.click();
		setTimeout(() => {
			URL.revokeObjectURL(url);
		}, 1000);
		toast.success(`Downloaded trace-${traceId.slice(0, 8)}.json`);
	};
	return (
		<header
			data-slot="trace-header"
			className="grid shrink-0 grid-cols-[minmax(0,1fr)_auto] items-start gap-x-7 gap-y-3 border-b border-line py-3.5 pr-4 pl-6 min-[1180px]:grid-cols-[minmax(0,1fr)_minmax(0,520px)_auto]"
		>
			<div className="col-start-1 row-start-1 flex min-w-0 flex-col gap-2">
				<div className="flex min-w-0 flex-wrap items-center gap-2">
					{trace !== undefined && (
						<Badge tone={trace.errors > 0 ? "bad" : "ok"}>{trace.errors > 0 ? "Error" : "OK"}</Badge>
					)}
					<SheetTitle className="min-w-0 truncate font-mono">
						{trace === undefined || trace.root.name === "" ? "Trace" : trace.root.name}
					</SheetTitle>
					<Tooltip>
						<TooltipTrigger
							render={
								<Button
									variant="ghost"
									size="sm"
									className="tv-tid-c group/tid ml-2 h-6 gap-[7px] rounded-[6px] border-0 bg-panel2 pr-[7px] pl-2 text-[12px] text-foreground shadow-[inset_0_0_0_1px_var(--line)] hover:shadow-[inset_0_0_0_1px_var(--line2)] focus-visible:shadow-[inset_0_0_0_1px_var(--line2)] focus-visible:ring-0 in-data-[theme=neobrutal]:rounded-[5px] in-data-[theme=neobrutal]:border-2 in-data-[theme=neobrutal]:border-(--ink) in-data-[theme=neobrutal]:bg-background in-data-[theme=neobrutal]:shadow-none in-data-[theme=neobrutal]:focus-visible:border-(--ink) in-data-[theme=neobrutal]:focus-visible:bg-panel2 in-data-[theme=neobrutal]:focus-visible:shadow-none"
									aria-label={`Copy trace ID ${traceId}`}
									onClick={() => {
										copy("trace", traceId);
									}}
								/>
							}
						>
							<span className="text-[11.5px] text-fg3">Trace</span>
							<span className="font-mono">
								{traceId.slice(0, 8)}
								<span className="text-fg3">…</span>
							</span>
							<span className="text-fg3 group-hover/tid:text-foreground">
								{copied === "trace" ? <CheckIcon /> : <CopyIcon />}
							</span>
						</TooltipTrigger>
						<TooltipContent className="font-mono">{traceId}</TooltipContent>
					</Tooltip>
				</div>
				<SheetDescription className={trace === undefined ? "sr-only" : "text-[12px] text-fg3"}>
					{trace === undefined ? (
						"Trace details"
					) : (
						<MetaLine>
							<span className="font-mono text-[13px] font-medium text-foreground">{durationLabel(trace.duration)}</span>
							{shares.length === 1 &&
								(resource === undefined ? (
									<span>{traceServiceLabel(trace.root)}</span>
								) : (
									<Link
										className="inline-flex items-center gap-1.5 text-foreground"
										to={resourcePath(orgId, workspaceId, resource.id)}
									>
										<i
											className="size-2 rounded-full"
											style={{
												background: color(trace.root),
											}}
										/>
										{rootIdentity?.name}
										{rootIdentity?.service !== undefined && <span className="text-fg3">({rootIdentity.service})</span>}
									</Link>
								))}
							<span title={formatFullTime(start, true, timePref)}>
								<span className="font-mono">{`${formatMonthDay(start, timePref)}, ${formatClock(start, true, timePref)} ${timeZoneLabel(start, timePref)}`}</span>
								<span className="ml-1.5">({agoLabel(start, now)})</span>
							</span>
							{linkCount > 0 && (
								<Button
									variant="plain-link"
									size="sm"
									className="h-auto gap-1.5 p-0 text-[13px] text-fg2 hover:text-foreground"
									onClick={onLinks}
								>
									<LinkIcon />
									{linkCount} linked {linkCount === 1 ? "span" : "spans"}
								</Button>
							)}
							{arriving && <span role="status">Spans may still be arriving</span>}
							{pinned !== null && (
								<Button
									variant="plain-link"
									size="sm"
									className="h-auto p-0 text-[12px]"
									onClick={() => {
										onPin(null);
									}}
								>
									Show all resources
								</Button>
							)}
						</MetaLine>
					)}
				</SheetDescription>
			</div>
			{items.length > 1 && (
				<div
					role="group"
					aria-label="Time spent per resource"
					className="col-span-2 col-start-1 row-start-2 flex min-w-0 flex-col gap-2 pt-1 min-[1180px]:col-span-1 min-[1180px]:col-start-2 min-[1180px]:row-start-1"
				>
					<div className="flex items-baseline gap-1.5 text-[11.5px] text-fg3">Time spent per resource</div>
					<div className="tv-mix flex h-2.5 gap-0.5 overflow-hidden rounded-[3px]">
						{top.map((item) => (
							<button
								type="button"
								key={item.key}
								{...interactions(item.key)}
								aria-pressed={pinned === item.key}
								aria-label={`${item.label}, ${durationLabel(item.nanos)} (${percent(item.share)})`}
								title={`${item.label}, ${durationLabel(item.nanos)} (${percent(item.share)})`}
								className={cn(
									"tv-mseg min-w-1 border-0 p-0 transition-opacity focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring focus-visible:outline-none",
									highlight !== null && highlight !== item.key && "opacity-25",
								)}
								style={{ flexGrow: Math.max(item.share, 0.012), background: item.color }}
							/>
						))}
						{rest.length > 0 && (
							<span
								className={cn(
									"tv-mseg min-w-1 bg-line2",
									highlight !== null && !rest.some((item) => item.key === highlight) && "opacity-25",
								)}
								style={{ flexGrow: Math.max(restShare, 0.012) }}
								title={`${rest.length.toString()} more, ${percent(restShare)}`}
							/>
						)}
					</div>
					<div className="flex flex-wrap gap-x-1 gap-y-0.5">
						{(expanded ? items : top).map((item) => (
							<button
								type="button"
								key={item.key}
								{...interactions(item.key)}
								title={`${item.label}, ${durationLabel(item.nanos)} (${percent(item.share)})`}
								aria-pressed={pinned === item.key}
								className={cn(
									"inline-flex h-[22px] min-w-0 items-center gap-1.5 rounded-sm px-1.5 text-left text-[12.5px] transition-opacity hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
									pinned === item.key && "bg-panel2",
									highlight !== null && highlight !== item.key && "opacity-40",
								)}
							>
								<i className="size-2 shrink-0 rounded-[2px]" style={{ background: item.color }} />
								<span className="truncate">
									{item.name}
									{item.service !== undefined && <span className="text-fg3"> ({item.service})</span>}
								</span>
								<span className="shrink-0 font-mono text-[11.5px] text-fg3">
									{durationLabel(item.nanos)} ({percent(item.share)})
								</span>
								{item.errors > 0 && (
									<span
										role="img"
										aria-label="Has errors"
										title="Has errors"
										className="size-1.5 shrink-0 rounded-full bg-err"
									/>
								)}
							</button>
						))}
						{rest.length > 0 && (
							<Button
								variant="ghost"
								size="sm"
								aria-expanded={expanded}
								className="h-[22px] px-1.5 text-[12.5px] text-fg3"
								onClick={() => {
									setExpanded(!expanded);
								}}
							>
								{expanded ? "Show fewer" : `+${rest.length.toString()} more`}
							</Button>
						)}
					</div>
				</div>
			)}
			<div className="col-start-2 row-start-1 flex min-[1180px]:col-start-3">
				{trace !== undefined && (
					<DropdownMenu>
						<DropdownMenuTrigger
							render={<Button variant="ghost" size="icon-sm" aria-label="More for this trace" title="More" />}
						>
							<EllipsisIcon />
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="w-[250px]">
							<DropdownMenuItem
								className="items-start"
								onClick={() => {
									goTo("logs", {
										range: aroundWindow(start, Date.now()),
										tokens: [{ key: "trace", value: traceId, neg: false }],
									});
								}}
							>
								<ScrollTextIcon />
								<MenuLabel sub="Filter logs by @trace">Logs for this trace</MenuLabel>
							</DropdownMenuItem>
							{resource !== undefined && (
								<DropdownMenuItem
									className="items-start"
									render={<Link to={resourcePath(orgId, workspaceId, resource.id)} />}
								>
									<BoxIcon />
									<MenuLabel sub="Resource page">Open {resource.raw.name}</MenuLabel>
								</DropdownMenuItem>
							)}
							<DropdownMenuItem
								className="items-start"
								onClick={() => {
									copy("json", traceJson(trace.spans), "Copied trace as JSON");
								}}
							>
								<BracesIcon />
								<MenuLabel sub={`${trace.spans.length.toString()} spans, OTLP shape`}>Copy as JSON</MenuLabel>
							</DropdownMenuItem>
							<DropdownMenuItem className="items-start" onClick={download}>
								<DownloadIcon />
								<MenuLabel sub={`trace-${traceId.slice(0, 8)}….json`}>Download JSON</MenuLabel>
							</DropdownMenuItem>
							<DropdownMenuItem
								className="items-start"
								onClick={() => {
									copy("link", window.location.href, "Copied link");
								}}
							>
								<LinkIcon />
								<MenuLabel sub="This trace and span">Copy link</MenuLabel>
							</DropdownMenuItem>
						</DropdownMenuContent>
					</DropdownMenu>
				)}
				<Button variant="ghost" size="icon-sm" aria-label="Close" onClick={onClose}>
					<XIcon />
				</Button>
			</div>
		</header>
	);
}

function MenuLabel({ sub, children }: { sub: string; children: ReactNode }) {
	return (
		<span className="flex min-w-0 flex-col">
			{children}
			<span className="text-[11.5px] text-fg3">{sub}</span>
		</span>
	);
}
