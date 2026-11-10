import type { TraceSpanEvent } from "@gen/loco/observability/v1/observability_pb";

import { Badge } from "@/components/design/Badge";
import { CopyButton } from "@/components/design/CopyButton";
import { maybeTsMs } from "@/lib/time";

import { fmtTs } from "./format";

export function SpanEvents({ events }: { events: TraceSpanEvent[] }) {
	return (
		<section aria-label="Span events" className="flex min-w-0 flex-col gap-2.5">
			<h3 className="m-0 text-[13px] font-medium">Events · {events.length}</h3>
			{events.length === 0 && <p className="m-0 text-[12.5px] text-fg3">No events recorded for this span.</p>}
			<ol className="m-0 flex list-none flex-col gap-3 p-0">
				{events.map((event, index) => {
					const timestamp = maybeTsMs(event.timestamp);
					const stack = event.attributes["exception.stacktrace"];
					return (
						<li key={`${index.toString()}:${event.name}`} className="min-w-0 rounded-lg border border-line p-3">
							<div className="mb-2 flex flex-wrap items-center gap-2">
								<Badge tone={event.name === "exception" ? "bad" : "muted"}>{event.name || "Unnamed event"}</Badge>
								<span className="font-mono text-[11.5px] text-fg3">
									{timestamp === undefined ? "Time unavailable" : fmtTs(timestamp, true)}
								</span>
							</div>
							<dl className="m-0">
								{Object.entries(event.attributes)
									.filter(([key]) => key !== "exception.stacktrace")
									.map(([key, value]) => (
										<div
											key={key}
											className="flex flex-wrap gap-x-3 gap-y-1 border-b border-line py-1.5 font-mono text-[12px] last:border-0"
										>
											<dt className="break-all text-fg3">{key}</dt>
											<dd className="m-0 min-w-0 whitespace-pre-wrap break-words text-foreground">{value}</dd>
										</div>
									))}
							</dl>
							{stack !== undefined && stack !== "" && (
								<div className="mt-2 min-w-0">
									<div className="flex items-center justify-between gap-2 text-[12px] text-fg3">
										Stack trace
										<CopyButton text={stack} name="Copy stack trace" />
									</div>
									<pre className="m-0 max-h-[360px] overflow-auto rounded-sm bg-panel p-2 font-mono text-[12px] whitespace-pre-wrap [overflow-wrap:anywhere]">
										{stack}
									</pre>
								</div>
							)}
						</li>
					);
				})}
			</ol>
		</section>
	);
}
