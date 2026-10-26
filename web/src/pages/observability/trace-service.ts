import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

const SERVICE_COLORS = 8;
const HASH_MULTIPLIER = 31;

export function traceServiceColor(name: string): string {
	let value = 0;
	for (const character of name) value = (value * HASH_MULTIPLIER + character.charCodeAt(0)) % SERVICE_COLORS;
	return `var(--trace-service-${String(value)})`;
}

export function traceServiceLabel(span: TraceSpan, resourceName?: string): string {
	if (resourceName !== undefined && span.serviceName === `resource-${span.resourceId}`) return resourceName;
	return span.serviceName || "Unknown service";
}
