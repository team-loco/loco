import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { ConfigService } from "@gen/loco/config/v1/config_pb";

import { nonEmpty } from "@/lib/utils";

import type { AuthAdapter } from "./adapters/types";

const BASE_URL = nonEmpty(import.meta.env.VITE_API_URL, "http://localhost:8000");

async function loadAdapter(): Promise<AuthAdapter> {
	const transport = createConnectTransport({ baseUrl: BASE_URL, useBinaryFormat: false });
	const { auth } = await createClient(ConfigService, transport).getConfig({});
	if (auth === undefined) throw new Error("This Loco server has no identity provider configured");
	switch (auth.adapter) {
		case "supabase": {
			const { createSupabaseAdapter } = await import("./adapters/supabase");
			return await createSupabaseAdapter(auth.url);
		}
		case "oidc": {
			const { createOIDCAdapter } = await import("./adapters/oidc");
			return await createOIDCAdapter({ issuer: auth.issuer, clientId: auth.clientId, scopes: auth.scopes });
		}
		default:
			throw new Error(`Unknown sign-in adapter "${auth.adapter}"`);
	}
}

let adapterPromise: Promise<AuthAdapter> | null = null;

export async function authAdapter(): Promise<AuthAdapter> {
	adapterPromise ??= loadAdapter().catch((err: unknown) => {
		adapterPromise = null;
		throw err;
	});
	return await adapterPromise;
}

export function signInRedirectURL(): string {
	return `${window.location.origin}/auth/callback`;
}
