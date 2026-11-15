import { ObservabilityService } from "@gen/loco/observability/v1/observability_pb";
import { EmptyHero } from "@/components/design/EmptyHero";
import { ErrorState } from "@/components/design/ErrorState";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { Spinner } from "@/components/design/Spinner";
import { useState } from "react";

import { useQueryState } from "nuqs";

import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ScrollTextIcon } from "lucide-react";

import { Button } from "@/components/design/Button";

import { Sheet, SheetBody, SheetContent } from "@/components/design/Sheet";

import { TracePlot } from "./trace-viewer/TracePlot";
import { TraceLogs } from "./trace-viewer/TraceLogs";
import { SpanPanel } from "./trace-viewer/SpanPanel";
import { TraceColors } from "./trace-viewer/TraceColors";
import { TraceHeader } from "./trace-viewer/TraceHeader";

import { useObs } from "./context";
import { FULL_ZOOM, type Zoom } from "./flame";

import { traceServiceLabel } from "./trace-service";
import { type TraceSummary } from "./traces";

export function TraceDrawer({
	open,
	traceId,
	trace,
	loading,
	error,
	onRetry,
	onClose,
}: {
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
	const [zoom, setZoom] = useState<Zoom>(FULL_ZOOM);
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
					<SheetBody className="gap-4">
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
							<div className="grid min-w-0 gap-5 min-[1100px]:grid-cols-[minmax(0,1fr)_400px]">
								<div className="flex min-w-0 flex-col gap-4">
									<TracePlot
										trace={trace}
										selected={selected}
										onSelect={select}
										label={label}
										zoom={zoom}
										onZoom={setZoom}
										highlight={highlight}
									/>
									<TraceLogs trace={trace} />
								</div>
								<SpanPanel trace={trace} selected={selected} missing={missing} select={select} label={label} />
							</div>
						)}
					</SheetBody>
				</SheetContent>
			</Sheet>
		</TraceColors>
	);
}
