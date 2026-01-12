import { createConnectTransport } from "@connectrpc/connect-web";

export const createTransport = () => {
	return createConnectTransport({
		baseUrl: "http://localhost:8000",
		fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
		useBinaryFormat: true,
	});
};

export const transport = createTransport();
