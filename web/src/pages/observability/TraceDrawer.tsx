import { useQuery } from "@connectrpc/connect-query";
import { ObservabilityService } from "@gen/loco/observability/v1/observability_pb";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { EmptyHero } from "@/components/design/EmptyHero";
import { ErrorState } from "@/components/design/ErrorState";
import { Spinner } from "@/components/design/Spinner";
import { useRef, useState, type KeyboardEventHandler } from "react";

import { useQueryState } from "nuqs";

import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ScrollTextIcon } from "lucide-react";

import { Button } from "@/components/design/Button";

import { Sheet, SheetBody, SheetContent } from "@/components/design/Sheet";

import { TraceLogs } from "./trace-viewer/TraceLogs";
import { TraceColors } from "./trace-viewer/TraceColors";
import { TraceHeader } from "./trace-viewer/TraceHeader";

import { useObs } from "./context";
import { useTraceZoom } from "./trace-viewer/useTraceZoom";

import { traceServiceLabel } from "./trace-service";
import { TRACE_ID_PATTERN, type TraceSummary } from "./traces";

export function TraceDrawer({
	onKeyDown,
	open,
	onClose,
	...view
}: {
	onKeyDown: KeyboardEventHandler<HTMLDivElement>;
	open: boolean;
	traceId: string;
	trace: TraceSummary | undefined;
	arriving: boolean;
	loading: boolean;
	error: Error | null;
	onRetry: () => Promise<unknown>;
	onClose: () => void;
}) {
	const popupRef = useRef<HTMLDivElement>(null);
	return (
		<Sheet
			open={open}
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
		>
			<SheetContent
				ref={popupRef}
				initialFocus={popupRef}
				onKeyDown={onKeyDown}
				size="full"
				className="outline-none data-[side=right]:w-[min(1480px,calc(100vw-16px))]"
				showCloseButton={false}
			>
				<TraceView key={view.traceId} {...view} onClose={onClose} />
			</SheetContent>
		</Sheet>
	);
}

function TraceView({
	traceId,
	trace,
	arriving,
	loading,
	error,
	onRetry,
	onClose,
}: {
	traceId: string;
	trace: TraceSummary | undefined;
	arriving: boolean;
	loading: boolean;
	error: Error | null;
	onRetry: () => Promise<unknown>;
	onClose: () => void;
}) {
	const { workspaceId, scopedResourceIds, resourceById } = useObs();
	// Only workspace readers may read its retention.
	const traceDays = useQuery(getWorkspace, { workspaceId }, { enabled: scopedResourceIds === null }).data?.workspace
		?.effectiveRetention?.traceDays;
	const { zoom, change: setZoom, drawerRef } = useTraceZoom();
	const [pinned, setPinned] = useState<string | null>(null);
	const [hovered, setHovered] = useState<string | null>(null);
	const highlight = hovered ?? pinned;
	const validId = TRACE_ID_PATTERN.test(traceId);
	const [spanId, setSpanId] = useQueryState("span", { history: "replace" });
	const linked = trace?.spans.find((s) => s.spanId === spanId);
	const selected = linked ?? trace?.root;
	const missing = spanId !== null && trace !== undefined && linked === undefined;
	const label = (span: TraceSpan) => traceServiceLabel(span, resourceById.get(span.resourceId)?.slug);
	const select = (span: TraceSpan) => {
		void setSpanId(span.spanId);
	};
	// The span detail tab belongs to one span; selecting another opens its Attributes.
	const [spanTab, setSpanTab] = useState({ spanId: "", tab: "Attributes" });
	const openLinks = () => {
		const span = trace?.spans.find((s) => s.links.length > 0);
		if (span === undefined) return;
		select(span);
		setSpanTab({ spanId: span.spanId, tab: "Links" });
	};
	return (
		<TraceColors trace={trace}>
			<div ref={drawerRef} className="contents">
				<TraceHeader
					trace={trace}
					traceId={traceId}
					arriving={arriving}
					onClose={onClose}
					highlight={highlight}
					pinned={pinned}
					onHover={setHovered}
					onPin={setPinned}
					onLinks={openLinks}
				/>
				<SheetBody className="gap-0 p-0 min-[1100px]:overflow-hidden">
					{loading && (
						<div role="status" className="flex min-h-[200px] items-center justify-center gap-2 text-fg3">
							<Spinner className="size-4" />
							Loading spans…
						</div>
					)}
					{!loading && error !== null && (
						<div className="px-6 py-5">
							<ErrorState
								error={error}
								what="this trace"
								rpc={ObservabilityService.method.getTrace}
								onRetry={onRetry}
							/>
						</div>
					)}
					{!loading && error === null && trace === undefined && (
						<div className="px-6 py-5">
							<EmptyHero
								art={<ScrollTextIcon className="size-8 text-fg3" />}
								title={validId ? "This trace is no longer available" : "Trace not found"}
								subtitle={
									validId
										? `${traceDays === undefined ? "" : `Traces are kept for ${String(traceDays)} days. `}It may also have been dropped before it was stored.`
										: "The trace ID looks invalid. A trace ID is 32 lowercase hexadecimal characters."
								}
							>
								<Button variant="outline" onClick={onClose}>
									Back to traces
								</Button>
							</EmptyHero>
						</div>
					)}
					{trace !== undefined && selected !== undefined && (
						<TraceLogs
							trace={trace}
							selected={selected}
							missing={missing}
							select={select}
							label={label}
							zoom={zoom}
							onZoom={setZoom}
							highlight={highlight}
							spanTab={spanTab.spanId === selected.spanId ? spanTab.tab : "Attributes"}
							onSpanTab={(tab) => {
								setSpanTab({ spanId: selected.spanId, tab });
							}}
						/>
					)}
				</SheetBody>
			</div>
		</TraceColors>
	);
}
