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
import { useState } from "react";
import { Link } from "react-router";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { SheetTitle, SheetDescription } from "@/components/design/Sheet";
import { useCopy } from "@/hooks/useCopy";
import { agoLabel } from "@/lib/time";
import { aroundWindow } from "@/lib/obs-time";
import { resourcePath } from "@/lib/routes";
import { cn } from "@/lib/utils";
import { useObs } from "../context";
import { useTraceResources } from "./TraceColors";
import { fmtTs } from "../format";
import { replicaOf } from "../rows";
import { durationLabel, spanError, type TraceSummary } from "../traces";
import { traceResourceKey, traceServiceLabel } from "../trace-service";
import { traceJson } from "./trace-json";

export function TraceHeader({
	trace,
	traceId,
	onClose,
	highlight,
	pinned,
	onHover,
	onPin,
}: {
	trace: TraceSummary | undefined;
	traceId: string;
	onClose: () => void;
	highlight: string | null;
	pinned: string | null;
	onHover: (service: string | null) => void;
	onPin: (service: string | null) => void;
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
					return {
						...share,
						name: resourceById.get(first.resourceId)?.name ?? traceServiceLabel(first),
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
	const root = trace?.root;
	const statusCode =
		root?.spanAttributes["http.response.status_code"] ?? root?.spanAttributes["http.status_code"] ?? "";
	const rootPod = root?.resourceAttributes["k8s.pod.name"] ?? "";
	const rootRegion = root?.resourceAttributes["loco.region"] ?? "";
	const resource = trace === undefined ? undefined : resourceById.get(trace.root.resourceId);
	const start = trace === undefined ? 0 : Number(trace.start / 1_000_000n);
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
	};
	return (
		<header className="grid shrink-0 grid-cols-[minmax(0,1fr)_auto] items-start gap-x-7 gap-y-3 border-b border-line py-3.5 pr-4 pl-6 min-[1180px]:grid-cols-[minmax(0,1fr)_minmax(0,520px)_auto]">
			<div className="col-start-1 row-start-1 flex min-w-0 flex-col gap-2">
				<div className="flex min-w-0 flex-wrap items-center gap-2">
					{trace !== undefined && (
						<Badge tone={trace.errors > 0 ? "bad" : "ok"}>{trace.errors > 0 ? "Error" : "OK"}</Badge>
					)}
					{statusCode !== "" && (
						<Badge
							tone={Number(statusCode) >= 500 ? "bad" : "muted"}
							title="HTTP status of the root span"
							className="font-mono"
						>
							{statusCode}
						</Badge>
					)}
					<SheetTitle className="min-w-0 truncate font-mono">
						{trace === undefined || trace.root.name === "" ? "Trace" : trace.root.name}
					</SheetTitle>
					{trace !== undefined && <span className="font-mono text-fg3">{durationLabel(trace.duration)}</span>}
					<Button
						variant="ghost"
						size="sm"
						className="h-6 px-1.5 font-mono text-fg3"
						title="Copy trace ID"
						aria-label={`Copy trace ID ${traceId}`}
						onClick={() => {
							copy("trace", traceId);
						}}
					>
						{traceId.slice(0, 8)}…{traceId.slice(-4)}
						{copied === "trace" ? <CheckIcon /> : <CopyIcon />}
					</Button>
				</div>
				<SheetDescription
					className={
						trace === undefined ? "sr-only" : "flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12px]"
					}
				>
					{trace === undefined ? (
						"Trace details"
					) : (
						<>
							{shares.length === 1 &&
								(resource === undefined ? (
									<span>{traceServiceLabel(trace.root)}</span>
								) : (
									<Link
										className="inline-flex items-center gap-1.5 text-foreground hover:underline"
										to={resourcePath(orgId, workspaceId, resource.id)}
									>
										<i
											className="size-2 rounded-full"
											style={{
												background: color(trace.root),
											}}
										/>
										{resource.name}
									</Link>
								))}
							{rootPod !== "" && <span className="font-mono">{replicaOf(rootPod)}</span>}
							{rootRegion !== "" && <span>{rootRegion}</span>}
							<span title={fmtTs(start, true, timePref)}>{fmtTs(start, true, timePref)}</span>
							<span>{agoLabel(start, now)}</span>
							<span>
								{trace.spans.length} {trace.spans.length === 1 ? "span" : "spans"}
							</span>
						</>
					)}
					{pinned !== null && (
						<Button
							variant="link"
							size="sm"
							className="h-auto p-0 text-[12px]"
							onClick={() => {
								onPin(null);
							}}
						>
							Show all resources
						</Button>
					)}
				</SheetDescription>
			</div>
			{items.length > 1 && (
				<div
					role="group"
					aria-label="Time by resource"
					className="col-span-2 col-start-1 row-start-2 flex min-w-0 flex-col gap-2 pt-1 min-[1180px]:col-span-1 min-[1180px]:col-start-2 min-[1180px]:row-start-1"
				>
					<div className="flex items-baseline gap-1.5 text-[11.5px] text-fg3">
						Time by resource <span className="font-mono">{items.length}</span>
					</div>
					<div className="tv-mix flex h-2.5 gap-0.5 overflow-hidden rounded-[3px]">
						{top.map((item) => (
							<button
								type="button"
								key={item.key}
								{...interactions(item.key)}
								aria-pressed={pinned === item.key}
								aria-label={`${item.name} · ${durationLabel(item.nanos)} · ${percent(item.share)}`}
								title={`${item.name} · ${durationLabel(item.nanos)} · ${percent(item.share)}`}
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
								title={`${rest.length.toString()} more · ${percent(restShare)}`}
							/>
						)}
					</div>
					<div className="flex flex-wrap gap-x-1 gap-y-0.5">
						{(expanded ? items : top).map((item) => (
							<button
								type="button"
								key={item.key}
								{...interactions(item.key)}
								title={durationLabel(item.nanos)}
								aria-pressed={pinned === item.key}
								className={cn(
									"inline-flex h-[22px] min-w-0 items-center gap-1.5 rounded-sm px-1.5 text-left text-[12.5px] transition-opacity hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
									pinned === item.key && "bg-panel2",
									highlight !== null && highlight !== item.key && "opacity-40",
								)}
							>
								<i className="size-2 shrink-0 rounded-[2px]" style={{ background: item.color }} />
								<span className="truncate">{item.name}</span>
								<span className="shrink-0 font-mono text-[11.5px] text-fg3">{percent(item.share)}</span>
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
				<DropdownMenu>
					<DropdownMenuTrigger
						render={<Button variant="ghost" size="icon-sm" aria-label="More for this trace" title="More" />}
					>
						<EllipsisIcon />
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end">
						<DropdownMenuItem
							disabled={trace === undefined}
							onClick={() => {
								if (trace !== undefined) copy("json", traceJson(trace.spans), "Copied trace JSON");
							}}
						>
							<BracesIcon />
							Copy as JSON
						</DropdownMenuItem>
						<DropdownMenuItem disabled={trace === undefined} onClick={download}>
							<DownloadIcon />
							Download JSON
						</DropdownMenuItem>
						<DropdownMenuItem
							onClick={() => {
								copy("link", window.location.href, "Copied trace link");
							}}
						>
							<LinkIcon />
							Copy link
						</DropdownMenuItem>
						{resource !== undefined && (
							<DropdownMenuItem render={<Link to={resourcePath(orgId, workspaceId, resource.id)} />}>
								<BoxIcon />
								Open {resource.name}
							</DropdownMenuItem>
						)}
						<DropdownMenuItem
							onClick={() => {
								goTo("logs", {
									range: aroundWindow(start, Date.now()),
									tokens: [{ key: "trace", value: traceId, neg: false }],
								});
							}}
						>
							<ScrollTextIcon />
							Logs for this trace
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
				<Button variant="ghost" size="icon-sm" aria-label="Close" onClick={onClose}>
					<XIcon />
				</Button>
			</div>
		</header>
	);
}
