import { createClient } from "@connectrpc/connect";
import { ConfigService } from "@gen/loco/config/v1/config_pb";

import { transport } from "./connect-transport";
import { createAuthClient, type AuthClient } from "./oidc";

async function loadAuthClient(): Promise<AuthClient> {
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
