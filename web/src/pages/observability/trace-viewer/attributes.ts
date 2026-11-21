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

const GROUP_TITLES: Record<string, string> = {
	http: "HTTP",
	url: "URL",
	server: "Server",
	client: "Client",
	network: "Network",
	net: "Network",
	rpc: "RPC",
	db: "Database",
	messaging: "Messaging",
	peer: "Peer",
	cache: "Cache",
};

/** Groups attributes by their namespace, in the order each group first appears. */
export function attributeGroups(pairs: [string, string][]): [string, [string, string][]][] {
	const groups = new Map<string, [string, string][]>();
	for (const pair of pairs) {
		const title = GROUP_TITLES[pair[0].split(".")[0] ?? ""] ?? "Other";
		groups.set(title, [...(groups.get(title) ?? []), pair]);
	}
	return [...groups];
}
