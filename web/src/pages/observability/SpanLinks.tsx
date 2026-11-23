import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { Link, useLocation } from "react-router";

import { NavArrow } from "@/components/design/NavArrow";
import { cn } from "@/lib/utils";

import { spanLinkSearch } from "./traces";

export function SpanLinks({ span }: { span: TraceSpan }) {
	const { search } = useLocation();
	const { links } = span;
	return (
		<section aria-label="Span links" className="min-w-0">
			<ol className="m-0 flex list-none flex-col gap-3 p-0">
				{links.map((link, index) => {
					const target = spanLinkSearch(search, link.traceId, link.spanId);
					const body = (
						<>
							<span className="flex items-center text-[12px]">
								<span className="font-mono text-fg3 [overflow-wrap:anywhere]">
									{link.traceId === span.traceId ? "This trace" : link.traceId || "No trace ID"},{" "}
									{link.spanId || "no span ID"}
								</span>
								{target !== null && <NavArrow />}
							</span>
							<dl className="m-0 mt-2 empty:hidden">
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
						</>
					);
					const card = "block min-w-0 rounded-lg border border-line p-3";
					return (
						<li key={`${index.toString()}:${link.spanId}`} className="min-w-0">
							{target === null ? (
								<div className={card}>{body}</div>
							) : (
								<Link
									to={target}
									className={cn(
										card,
										"group/nav text-foreground no-underline hover:border-line2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
									)}
								>
									{body}
								</Link>
							)}
						</li>
					);
				})}
			</ol>
		</section>
	);
}
