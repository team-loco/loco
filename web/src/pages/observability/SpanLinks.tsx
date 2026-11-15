import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { Link, useLocation } from "react-router";

import { spanLinkSearch } from "./traces";

export function SpanLinks({ span }: { span: TraceSpan }) {
	const { search } = useLocation();
	const { links } = span;
	return (
		<section aria-label="Span links" className="flex min-w-0 flex-col gap-2.5">
			<h3 className="m-0 text-[13px] font-medium">Links · {links.length}</h3>
			{links.length === 0 && <p className="m-0 text-[12.5px] text-fg3">No links recorded for this span.</p>}
			<ol className="m-0 flex list-none flex-col gap-3 p-0">
				{links.map((link, index) => {
					const target = spanLinkSearch(search, link.traceId, link.spanId);
					return (
						<li key={`${index.toString()}:${link.spanId}`} className="min-w-0 rounded-lg border border-line p-3">
							<div className="mb-2 flex flex-wrap items-center justify-between gap-2 text-[12px]">
								<span className="font-mono text-fg3 [overflow-wrap:anywhere]">
									{link.traceId === span.traceId ? "This trace" : link.traceId || "No trace ID"} ·{" "}
									{link.spanId || "no span ID"}
								</span>
								{target !== null && (
									<Link
										to={target}
										className="rounded-sm text-primary underline underline-offset-2 hover:text-fg2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
									>
										Open linked span
									</Link>
								)}
							</div>
							<dl className="m-0">
								{Object.entries(link.attributes).map(([key, value]) => (
									<div
										key={key}
										className="flex flex-wrap gap-x-3 gap-y-1 border-b border-line py-1.5 font-mono text-[12px] last:border-0"
									>
										<dt className="break-all text-fg3">{key}</dt>
										<dd className="m-0 min-w-0 whitespace-pre-wrap break-words text-foreground">{value}</dd>
									</div>
								))}
							</dl>
						</li>
					);
				})}
			</ol>
		</section>
	);
}
