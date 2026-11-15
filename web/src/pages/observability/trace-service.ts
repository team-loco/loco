import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

const SERVICE_COLORS = 8;

export function traceServiceLabel(span: Pick<TraceSpan, "serviceName" | "resourceId">, resourceName?: string): string {
	if (resourceName !== undefined && span.serviceName === `resource-${span.resourceId}`) return resourceName;
	return span.serviceName || "Unknown service";
}

export function traceResourceKey(span: Pick<TraceSpan, "resourceId" | "serviceName">): string {
	return span.resourceId || span.serviceName;
}

export function traceColors(keys: string[]): Map<string, string> {
	return new Map(
		keys.map((key, rank) => {
			const base = `var(--series-${String(rank % SERVICE_COLORS)})`;
			const cycle = Math.floor(rank / SERVICE_COLORS);
			return [key, cycle === 0 ? base : `color-mix(in oklch, ${base} ${String(100 / (cycle + 1))}%, var(--fg))`];
		}),
	);
}
