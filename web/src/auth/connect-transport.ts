import { Code, ConnectError } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { nonEmpty } from "@/lib/utils";

import { authAdapter } from "./runtime";

const BASE_URL = nonEmpty(import.meta.env.VITE_API_URL, "http://localhost:8000");
const APP_ENV = nonEmpty(import.meta.env.VITE_APP_ENV, "DEVELOPMENT");

export const createTransport = (baseUrl: string = BASE_URL) => {
	return createConnectTransport({
		baseUrl,
		useBinaryFormat: APP_ENV === "PRODUCTION",
		interceptors: [
			(next) => async (req) => {
				if (req.url.includes("/ConfigService/")) return await next(req);
				const adapter = await authAdapter();
				const token = await adapter.getAccessToken();
				if (token !== null) req.header.set("Authorization", `Bearer ${token}`);
				try {
					return await next(req);
				} catch (err) {
					if (!(err instanceof ConnectError) || err.code !== Code.Unauthenticated || token === null) throw err;
					const fresh = await adapter.getAccessToken(true);
					if (fresh === null) throw err;
					req.header.set("Authorization", `Bearer ${fresh}`);
					return await next(req);
				}
			},
		],
	});
};

export const transport = createTransport();
