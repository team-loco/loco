interface ProxyConfig {
	upstream: string;
	key: string;
	origin: string;
}

export function stagingProxy(config: ProxyConfig) {
	return async (request: Request): Promise<Response> => {
		const url = new URL(request.url);
		const origin = request.headers.get("origin");
		const sameOriginFetch = request.headers.get("sec-fetch-site") === "same-origin";
		const allowedOrigin = origin === null ? sameOriginFetch : origin === config.origin;
		if (url.origin !== config.origin || !allowedOrigin) return new Response("Forbidden origin", { status: 403 });
		const headers = new Headers(request.headers);
		headers.set("authorization", `Bearer ${config.key}`);
		headers.delete("cookie");
		const path = url.pathname.slice("/api".length) + url.search;
		const response = await fetch(`${config.upstream}${path}`, {
			method: request.method,
			headers,
			body: request.body,
			redirect: "manual",
			decompress: false,
			signal: request.signal,
		});
		const responseHeaders = new Headers(response.headers);
		responseHeaders.delete("set-cookie");
		return new Response(response.body, { status: response.status, headers: responseHeaders });
	};
}
