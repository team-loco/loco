import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

export const ATTRIBUTE_TABS = ["Attributes", "Loco", "Kubernetes", "Telemetry"] as const;
export type AttributeTab = (typeof ATTRIBUTE_TABS)[number];
export function spanAttributes(span: TraceSpan): Record<AttributeTab, [string, string][]> {
	const result: Record<AttributeTab, [string, string][]> = {
		Attributes: Object.entries(span.spanAttributes),
		Loco: [],
		Kubernetes: [],
		Telemetry: [],
	};
	for (const pair of Object.entries(span.resourceAttributes)) {
		const key = pair[0];
		const tab = key.startsWith("loco.") ? "Loco" : /^(k8s|container|cloud)\./.test(key) ? "Kubernetes" : "Telemetry";
		result[tab].push(pair);
	}
	if (span.scopeName !== "") result.Telemetry.push(["scope.name", span.scopeName]);
	if (span.scopeVersion !== "") result.Telemetry.push(["scope.version", span.scopeVersion]);
	return result;
}
