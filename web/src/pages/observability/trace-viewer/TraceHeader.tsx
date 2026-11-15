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
import { serviceShares } from "../flame";
import { fmtTs } from "../format";
import { replicaOf } from "../rows";
import { durationLabel, spanError, type TraceSummary } from "../traces";
import { traceServiceColor, traceServiceLabel } from "../trace-service";
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
	const shares =
		trace === undefined
			? []
			: serviceShares(trace).toSorted((a, b) => (a.nanos === b.nanos ? 0 : a.nanos > b.nanos ? -1 : 1));
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
			<div className="flex min-w-0 flex-col gap-2">
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
						{trace === undefined || trace.root.name === ""
							? `Trace ${traceId.slice(0, 8)}…${traceId.slice(-4)}`
							: trace.root.name}
					</SheetTitle>
					{trace !== undefined && <span className="font-mono text-fg3">{durationLabel(trace.duration)}</span>}
					<Button
						variant="ghost"
						size="sm"
						className="h-6 px-1.5 font-mono text-fg3"
						title="Copy trace ID"
						onClick={() => {
							copy("trace", traceId);
						}}
					>
						{traceId.slice(0, 8)}…{traceId.slice(-4)}
						{copied === "trace" ? <CheckIcon /> : <CopyIcon />}
					</Button>
				</div>
				<SheetDescription className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12px]">
					{trace === undefined ? (
						traceId
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
												background: traceServiceColor(trace.root.serviceName),
											}}
										/>
										{resource.name}
									</Link>
								))}
							{rootPod !== "" && <span className="font-mono">{replicaOf(rootPod)}</span>}
							{rootRegion !== "" && <span>{rootRegion}</span>}
							<span title={fmtTs(start, true, timePref)}>{fmtTs(start, true, timePref)}</span>
							<span>{agoLabel(start, now)}</span>
							<span>{trace.spans.length} spans</span>
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
			{trace !== undefined && shares.length > 1 && (
				<div
					role="group"
					aria-label="Time by resource"
					className="col-span-2 row-start-2 flex min-w-0 flex-col gap-0.5 min-[1180px]:col-span-1 min-[1180px]:col-start-2 min-[1180px]:row-start-1"
				>
					{shares.map((s) => {
						const spans = trace.spans.filter((span) => span.serviceName === s.service);
						const first = spans[0];
						const errors = spans.filter(spanError).length;
						return (
							<button
								type="button"
								key={s.service}
								aria-pressed={pinned === s.service}
								onMouseEnter={() => {
									onHover(s.service);
								}}
								onMouseLeave={() => {
									onHover(null);
								}}
								onFocus={() => {
									onHover(s.service);
								}}
								onBlur={() => {
									onHover(null);
								}}
								onClick={() => {
									onPin(pinned === s.service ? null : s.service);
								}}
								className={cn(
									"grid min-w-0 grid-cols-[minmax(64px,1fr)_minmax(30px,1fr)_60px_34px] items-center gap-2 rounded-sm px-1.5 py-0.5 text-left text-[12px] hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none sm:grid-cols-[84px_minmax(40px,1fr)_60px_34px_110px]",
									pinned === s.service && "bg-panel2",
									highlight !== null && highlight !== s.service && "opacity-40",
								)}
							>
								<span className="flex min-w-0 items-center gap-1.5">
									<i className="size-2 shrink-0 rounded-full" style={{ background: traceServiceColor(s.service) }} />
									<span className="truncate">
										{first === undefined
											? s.service
											: traceServiceLabel(first, resourceById.get(first.resourceId)?.name)}
									</span>
								</span>
								<span className="h-1.5 overflow-hidden rounded-sm bg-panel2">
									<i
										className="block h-full rounded-sm"
										style={{
											width: `${String(s.share * 100)}%`,
											background: traceServiceColor(s.service),
										}}
									/>
								</span>
								<span className="text-right font-mono">{durationLabel(s.nanos)}</span>
								<span className="text-right font-mono text-fg3">{Math.round(s.share * 100)}%</span>
								<span className="hidden text-fg3 sm:block">
									{spans.length} spans
									{errors > 0 && <span className="text-bad-fg"> · {errors} errors</span>}
								</span>
							</button>
						);
					})}
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
							onClick={() => {
								copy("trace", traceId, "Copied trace ID");
							}}
						>
							<CopyIcon />
							Copy trace ID
						</DropdownMenuItem>
						<DropdownMenuItem
							disabled={trace === undefined}
							onClick={() => {
								if (trace !== undefined) copy("json", traceJson(trace.spans), "Copied trace JSON");
							}}
						>
							<BracesIcon />
							Copy as JSON
							{trace !== undefined && <span className="text-fg3">{trace.spans.length} spans</span>}
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
