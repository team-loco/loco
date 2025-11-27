import { createConnectTransport } from "@connectrpc/connect-web";
import { authManager } from "./auth-manager";

export const createTransport = () => {
	return createConnectTransport({
		baseUrl: "http://localhost:8000",
		fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
		interceptors: [
			(next) => (req) => {
				const token = authManager.getToken();
				console.log("interceptor", token);
				if (token) {
					req.header.set("Authorization", `Bearer ${token}`);
				}
				return next(req);
			},
		],
	});
};

export const transport = createTransport();
