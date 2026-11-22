import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

const SERVICE_COLORS = 8;

// traceIdentity names a span by its Loco resource, keeping a differing OTel service name as secondary.
export function traceIdentity(
	span: Pick<TraceSpan, "serviceName" | "resourceId">,
	resourceName?: string,
): { name: string; service: string | undefined } {
	if (resourceName === undefined) return { name: span.serviceName || "Unknown service", service: undefined };
	const own =
		span.serviceName === "" || span.serviceName === `resource-${span.resourceId}` || span.serviceName === resourceName;
	return { name: resourceName, service: own ? undefined : span.serviceName };
}

export function traceServiceLabel(span: Pick<TraceSpan, "serviceName" | "resourceId">, resourceName?: string): string {
	const { name, service } = traceIdentity(span, resourceName);
	return service === undefined ? name : `${name} (${service})`;
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
