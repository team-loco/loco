import { createClient, type Client, type Transport } from "@connectrpc/connect";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";

import { createTransport } from "@/auth/connect-transport";

const transports = new Map<string, Transport>();

function transportFor(url: string): Transport {
	const cached = transports.get(url);
	if (cached !== undefined) return cached;
	const t = createTransport(url);
	transports.set(url, t);
	return t;
}

export function proxyClient(url: string): Client<typeof ObservabilityProxyService> {
	return createClient(ObservabilityProxyService, transportFor(url));
}
