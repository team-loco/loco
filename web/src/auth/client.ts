import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { ConfigService } from "@gen/loco/config/v1/config_pb";

import { nonEmpty } from "@/lib/utils";

import { createAuthClient, type AuthClient } from "./oidc";

const BASE_URL = nonEmpty(import.meta.env.VITE_API_URL, "http://localhost:8000");

async function loadAuthClient(): Promise<AuthClient> {
	const transport = createConnectTransport({ baseUrl: BASE_URL, useBinaryFormat: false });
	const { auth } = await createClient(ConfigService, transport).getConfig({});
	if (auth === undefined) throw new Error("This Loco server has no identity provider configured");
	return await createAuthClient({ issuer: auth.issuer, clientId: auth.clientId, scopes: auth.scopes });
}

let clientPromise: Promise<AuthClient> | null = null;

export async function authClient(): Promise<AuthClient> {
	clientPromise ??= loadAuthClient().catch((err: unknown) => {
		clientPromise = null;
		throw err;
	});
	return await clientPromise;
}

export function signInRedirectURL(): string {
	return `${window.location.origin}/auth/callback`;
}
