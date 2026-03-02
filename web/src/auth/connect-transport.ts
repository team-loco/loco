import { OAuthService } from "@/gen/loco/oauth/v1/oauth_pb";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

const BASE_URL = import.meta.env.VITE_API_URL || "http://localhost:8000";

const withCreds = (input: RequestInfo | URL, init?: RequestInit) =>
	fetch(input, { ...init, credentials: "include" });

// Serialise concurrent refreshes so we only make one round-trip.
let refreshing: Promise<void> | null = null;

async function doRefresh(): Promise<void> {
	// Instantiate lazily to avoid module-level unsafe-call lint errors.
	// This transport has no interceptors to prevent refresh recursion.
	const authTransport = createConnectTransport({
		baseUrl: BASE_URL,
		fetch: withCreds,
		useBinaryFormat: false,
	});
	// server reads loco_refresh_token from the cookie; body can be empty.
	// eslint-disable-next-line @typescript-eslint/no-unsafe-call
	await createClient(OAuthService, authTransport).refreshToken({});
}

function refreshTokens(): Promise<void> {
	refreshing ??= doRefresh().finally(() => {
		refreshing = null;
	});
	return refreshing;
}

export const createTransport = (baseUrl: string = BASE_URL) => {
	return createConnectTransport({
		baseUrl,
		fetch: withCreds,
		useBinaryFormat: false,
		interceptors: [
			(next) => async (req) => {
				// Skip retry logic for OAuth endpoints to avoid recursion.
				if (req.url.includes("/OAuthService/")) return next(req);
				try {
					return await next(req);
				} catch (err) {
					if (
						err instanceof ConnectError &&
						err.code === Code.Unauthenticated
					) {
						try {
							await refreshTokens();
							return await next(req); // retry once with new cookie
						} catch {
							throw err; // refresh failed — surface original error
						}
					}
					throw err;
				}
			},
		],
	});
};

export const transport = createTransport();
