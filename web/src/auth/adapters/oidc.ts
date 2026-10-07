import * as oauth from "oauth4webapi";

import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

import type { AuthAdapter } from "./types";

const TOKENS_KEY = "loco:auth:oidc:v1";
const PENDING_KEY = "loco:auth:oidc:pending:v1";
const EXPIRY_SKEW_MS = 30_000;

interface StoredTokens {
	idToken: string;
	refreshToken: string | null;
	expiresAt: number;
}

interface PendingLogin {
	verifier: string;
	state: string;
	nonce: string;
	redirectTo: string;
}

export interface OIDCConfig {
	issuer: string;
	clientId: string;
	scopes: string;
}

function parseJSON<T>(raw: string | null): T | null {
	if (raw === null) return null;
	try {
		return JSON.parse(raw) as T;
	} catch {
		return null;
	}
}

function idTokenExpiry(idToken: string): number {
	const payload = idToken.split(".")[1];
	if (payload === undefined) return 0;
	const claims = parseJSON<{ exp?: number }>(atob(payload.replace(/-/g, "+").replace(/_/g, "/")));
	return (claims?.exp ?? 0) * 1000;
}

export async function createOIDCAdapter(cfg: OIDCConfig): Promise<AuthAdapter> {
	const issuer = new URL(cfg.issuer);
	const insecure = issuer.protocol === "http:";
	const requestOptions = { [oauth.allowInsecureRequests]: insecure };
	const as = await oauth.processDiscoveryResponse(
		issuer,
		await oauth.discoveryRequest(issuer, { algorithm: "oidc", ...requestOptions }),
	);
	const client: oauth.Client = { client_id: cfg.clientId };
	const clientAuth = oauth.None();
	const listeners = new Set<() => void>();
	let tokens = parseJSON<StoredTokens>(readStorage(TOKENS_KEY));
	let refreshing: Promise<string | null> | null = null;

	const notify = () => {
		for (const listener of listeners) listener();
	};

	const store = (next: StoredTokens | null) => {
		tokens = next;
		if (next === null) removeStorage(TOKENS_KEY);
		else writeStorage(TOKENS_KEY, JSON.stringify(next));
		notify();
	};

	const fromResponse = (res: oauth.TokenEndpointResponse, previousRefresh: string | null): StoredTokens => {
		if (res.id_token === undefined) throw new Error("The identity provider did not return an ID token");
		return {
			idToken: res.id_token,
			refreshToken: res.refresh_token ?? previousRefresh,
			expiresAt: idTokenExpiry(res.id_token),
		};
	};

	const refresh = async (): Promise<string | null> => {
		const current = tokens;
		if (current?.refreshToken == null) {
			store(null);
			return null;
		}
		try {
			const res = await oauth.processRefreshTokenResponse(
				as,
				client,
				await oauth.refreshTokenGrantRequest(as, client, clientAuth, current.refreshToken, requestOptions),
			);
			const next = fromResponse(res, current.refreshToken);
			store(next);
			return next.idToken;
		} catch {
			store(null);
			return null;
		}
	};

	return {
		kind: "oidc",
		hasSession: () => tokens !== null,
		subscribe: (onChange) => {
			listeners.add(onChange);
			return () => {
				listeners.delete(onChange);
			};
		},
		getAccessToken: async (forceRefresh = false) => {
			if (tokens === null) return null;
			if (!forceRefresh && tokens.expiresAt - EXPIRY_SKEW_MS > Date.now()) return tokens.idToken;
			refreshing ??= refresh().finally(() => {
				refreshing = null;
			});
			return await refreshing;
		},
		loginMethods: async () => await Promise.resolve([{ kind: "redirect", label: "Continue with single sign-on" }]),
		signInWithOAuth: async () => {
			await Promise.reject(new Error("This sign-in method is not available"));
		},
		signInWithEmail: async () => {
			await Promise.reject(new Error("This sign-in method is not available"));
		},
		signInWithSSO: async () => {
			await Promise.reject(new Error("This sign-in method is not available"));
		},
		signInWithRedirect: async (redirectTo) => {
			if (as.authorization_endpoint === undefined) throw new Error("The identity provider has no authorization endpoint");
			const pending: PendingLogin = {
				verifier: oauth.generateRandomCodeVerifier(),
				state: oauth.generateRandomState(),
				nonce: oauth.generateRandomNonce(),
				redirectTo,
			};
			writeStorage(PENDING_KEY, JSON.stringify(pending), "session");
			const url = new URL(as.authorization_endpoint);
			url.searchParams.set("client_id", cfg.clientId);
			url.searchParams.set("redirect_uri", redirectTo);
			url.searchParams.set("response_type", "code");
			url.searchParams.set("scope", cfg.scopes);
			url.searchParams.set("code_challenge", await oauth.calculatePKCECodeChallenge(pending.verifier));
			url.searchParams.set("code_challenge_method", "S256");
			url.searchParams.set("state", pending.state);
			url.searchParams.set("nonce", pending.nonce);
			window.location.assign(url.toString());
		},
		completeRedirect: async (current) => {
			const pending = parseJSON<PendingLogin>(readStorage(PENDING_KEY, "session"));
			removeStorage(PENDING_KEY, "session");
			if (pending === null) throw new Error("This sign-in link has expired. Try signing in again.");
			const params = oauth.validateAuthResponse(as, client, current, pending.state);
			const res = await oauth.processAuthorizationCodeResponse(
				as,
				client,
				await oauth.authorizationCodeGrantRequest(
					as,
					client,
					clientAuth,
					params,
					pending.redirectTo,
					pending.verifier,
					requestOptions,
				),
				{ expectedNonce: pending.nonce, requireIdToken: true },
			);
			store(fromResponse(res, null));
		},
		verifyEmailLink: async () => {
			await Promise.reject(new Error("Email links are not used with this identity provider"));
		},
		signOut: async () => {
			store(null);
			await Promise.resolve();
		},
	};
}
