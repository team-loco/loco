import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@gen/loco/auth/v1/auth_pb";
import { ConfigService } from "@gen/loco/config/v1/config_pb";
import { TokenService } from "@gen/loco/token/v1/token_pb";
import { PUBLIC_API_URL, PUBLIC_APP_ENV } from "loco:env";

import { resolveAPIURL } from "./api-url";

export const BASE_URL = resolveAPIURL(window.locoConfig?.apiURL, PUBLIC_API_URL, window.location.origin);

const options = {
	baseUrl: BASE_URL,
	useBinaryFormat: PUBLIC_APP_ENV === "PRODUCTION",
	fetch: async (input: RequestInfo | URL, init?: RequestInit) =>
		await fetch(input, { ...init, credentials: "include" }),
};
const sessionTransport = createConnectTransport(options);
const sessions = createClient(AuthService, sessionTransport);
const tokens = createClient(TokenService, sessionTransport);

export const unauthenticated = (err: unknown) => err instanceof ConnectError && err.code === Code.Unauthenticated;

async function withSessionLock<T>(run: () => Promise<T>): Promise<T> {
	// The browser shares cookies with other tabs. Refresh and sign-out must use
	// the latest cookie pair so concurrent refreshes do not look like replay.
	const { locks } = navigator as { locks?: LockManager };
	return locks === undefined ? await run() : await locks.request("loco:session", run);
}

let refreshing: Promise<void> | undefined;
let sessionGeneration = 0;

async function refreshSession(generation: number): Promise<void> {
	if (generation !== sessionGeneration) return;
	refreshing ??= withSessionLock(async () => {
		try {
			await tokens.getScopes({});
		} catch (err) {
			if (!unauthenticated(err)) throw err;
			await sessions.refreshWebSession({});
		}
		sessionGeneration++;
	}).finally(() => {
		refreshing = undefined;
	});
	await refreshing;
}

export const authClient = sessions;
export const signOut = async () => await withSessionLock(async () => await sessions.signOut({}));

export const transport = createConnectTransport({
	...options,
	interceptors: [
		(next) => async (req) => {
			// Connect-Web sets the version on unary calls only.
			req.header.set("Connect-Protocol-Version", "1");
			const generation = sessionGeneration;
			if (req.service === ConfigService) return await next(req);

			if (req.stream) {
				// Connect-Web only sends one input message in a server stream.
				// Reuse that message if auth fails before the first result.
				const inputMessage = req.message[Symbol.asyncIterator]().next();
				const replay = async function* () {
					const input = await inputMessage;
					if (!input.done) yield input.value;
				};
				const open = async () => {
					const response = await next({ ...req, message: replay() });
					if (!response.stream) throw new Error("Expected a streaming response");
					return response;
				};
				let response;
				try {
					response = await open();
				} catch (err) {
					if (!unauthenticated(err)) throw err;
					await refreshSession(generation);
					return await open();
				}
				const original = response.message;
				return {
					...response,
					message: (async function* () {
						let started = false;
						try {
							for await (const message of original) {
								started = true;
								yield message;
							}
						} catch (err) {
							if (started || !unauthenticated(err)) throw err;
							await refreshSession(generation);
							yield* (await open()).message;
						}
					})(),
				};
			}
			try {
				return await next(req);
			} catch (err) {
				if (!unauthenticated(err)) throw err;
				await refreshSession(generation);
				return await next(req);
			}
		},
	],
});
