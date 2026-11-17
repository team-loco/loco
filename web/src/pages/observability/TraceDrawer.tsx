import { ObservabilityService } from "@gen/loco/observability/v1/observability_pb";
import { EmptyHero } from "@/components/design/EmptyHero";
import { ErrorState } from "@/components/design/ErrorState";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { Spinner } from "@/components/design/Spinner";
import { useState, type KeyboardEventHandler } from "react";

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
import { type TraceSummary } from "./traces";

export function TraceDrawer({
	onKeyDown,
	open,
	traceId,
	trace,
	loading,
	error,
	onRetry,
	onClose,
}: {
	onKeyDown: KeyboardEventHandler<HTMLDivElement>;
	open: boolean;
	traceId: string;
	trace: TraceSummary | undefined;
	loading: boolean;
	error: Error | null;
	onRetry: () => Promise<unknown>;
	onClose: () => void;
}) {
	const { orgId, resourceById } = useObs();
	const limits = useOrgLimits(orgId);
	const { zoom, change: setZoom, drawerRef } = useTraceZoom();
	const [pinned, setPinned] = useState<string | null>(null);
	const [hovered, setHovered] = useState<string | null>(null);
	const highlight = hovered ?? pinned;
	const [spanId, setSpanId] = useQueryState("span", { history: "replace" });
	const linked = trace?.spans.find((s) => s.spanId === spanId);
	const selected = linked ?? trace?.root;
	const missing = spanId !== null && trace !== undefined && linked === undefined;
	const label = (span: TraceSpan) => traceServiceLabel(span, resourceById.get(span.resourceId)?.name);
	const select = (span: TraceSpan) => {
		void setSpanId(span.spanId);
	};
	return (
		<TraceColors trace={trace}>
			<Sheet
				open={open}
				onOpenChange={(next) => {
					if (!next) onClose();
				}}
			>
				<SheetContent
					onKeyDown={onKeyDown}
					ref={drawerRef}
					size="full"
					className="data-[side=right]:w-[min(1480px,calc(100vw-16px))]"
					showCloseButton={false}
				>
					<TraceHeader
						trace={trace}
						traceId={traceId}
						onClose={onClose}
						highlight={highlight}
						pinned={pinned}
						onHover={setHovered}
						onPin={setPinned}
					/>
					<SheetBody className="gap-4 min-[1100px]:overflow-hidden">
						{loading && (
							<div role="status" className="flex min-h-[200px] items-center justify-center gap-2 text-fg3">
								<Spinner className="size-4" />
								Loading spans…
							</div>
						)}
						{!loading && error !== null && (
							<ErrorState
								error={error}
								what="this trace"
								rpc={ObservabilityService.method.getTrace}
								onRetry={onRetry}
							/>
						)}
						{!loading && error === null && trace === undefined && (
							<EmptyHero
								art={<ScrollTextIcon className="size-8 text-fg3" />}
								title="This trace is no longer available"
								subtitle={`${limits === undefined ? "" : `Traces are kept for ${String(limits.traceRetentionDays)} days. `}It may also have been dropped before it was stored.`}
							>
								<Button variant="outline" onClick={onClose}>
									Back to traces
								</Button>
							</EmptyHero>
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
							/>
						)}
					</SheetBody>
				</SheetContent>
			</Sheet>
		</TraceColors>
	);
}
