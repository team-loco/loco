import { createConnectTransport } from "@connectrpc/connect-web";
import type { Transport } from "@connectrpc/connect";

export function createObsTransport(proxyUrl: string, token: string): Transport {
	return createConnectTransport({
		baseUrl: proxyUrl,
		useBinaryFormat: false,
		interceptors: [
			(next) => async (req) => {
				req.header.set("Authorization", `Bearer ${token}`);
				return next(req);
			},
		],
	});
}
