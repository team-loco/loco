import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { spanStart, timestampNanos } from "../traces";

const attributes = (values: Record<string, string>) =>
	Object.entries(values).map(([key, value]) => ({
		key,
		value: { stringValue: value },
	}));
const KINDS: Record<string, number> = { internal: 1, server: 2, client: 3, producer: 4, consumer: 5 };
const kind = (value: string) => KINDS[value.toLowerCase()] ?? 0;
const status = (value: string) => (value.toLowerCase() === "error" ? 2 : value.toLowerCase() === "ok" ? 1 : 0);

export function traceJson(spans: TraceSpan[]): string {
	return JSON.stringify(
		{
			resourceSpans: spans.map((span) => ({
				resource: { attributes: attributes(span.resourceAttributes) },
				scopeSpans: [
					{
						scope: { name: span.scopeName, version: span.scopeVersion },
						spans: [
							{
								traceId: span.traceId,
								spanId: span.spanId,
								parentSpanId: span.parentSpanId,
								name: span.name,
								kind: kind(span.kind),
								startTimeUnixNano: String(spanStart(span)),
								endTimeUnixNano: String(spanStart(span) + span.durationNanos),
								attributes: attributes(span.spanAttributes),
								status: {
									code: status(span.statusCode),
									message: span.statusMessage,
								},
								events: span.events.map((event) => ({
									name: event.name,
									timeUnixNano: String(timestampNanos(event.timestamp)),
									attributes: attributes(event.attributes),
								})),
								links: span.links.map((link) => ({
									traceId: link.traceId,
									spanId: link.spanId,
									attributes: attributes(link.attributes),
								})),
							},
						],
					},
				],
			})),
		},
		null,
		2,
	);
}
