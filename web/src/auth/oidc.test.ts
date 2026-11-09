import { afterEach, describe, expect, mock, spyOn, test } from "bun:test";

import { createAuthClient, type AuthClient } from "./oidc";

const ISSUER = "https://idp.loco.test";
const REDIRECT = "https://app.loco.test/auth/callback";

function memoryStorage(): Pick<Storage, "getItem" | "setItem" | "removeItem"> {
	const items = new Map<string, string>();
	return {
		getItem: (key) => items.get(key) ?? null,
		setItem: (key, value) => {
			items.set(key, value);
		},
		removeItem: (key) => {
			items.delete(key);
		},
	};
}

async function startSignIn(loginHint: string | null): Promise<{ client: AuthClient; url: URL }> {
	spyOn(globalThis, "fetch").mockResolvedValue(
		Response.json({ issuer: ISSUER, authorization_endpoint: `${ISSUER}/authorize` }),
	);
	const assign = mock((_url: string) => undefined);
	Object.defineProperty(globalThis, "window", {
		value: { location: { assign }, sessionStorage: memoryStorage() },
		configurable: true,
	});
	const client = await createAuthClient({ issuer: ISSUER, clientId: "loco-web", scopes: "openid email" });
	await client.signIn(REDIRECT, loginHint);
	const target = assign.mock.calls[0]?.[0];
	if (target === undefined) throw new Error("sign-in did not redirect");
	return { client, url: new URL(target) };
}

function callback(params: Record<string, string>): URL {
	const url = new URL(REDIRECT);
	for (const [key, value] of Object.entries(params)) url.searchParams.set(key, value);
	return url;
}

describe("sign-in redirect", () => {
	afterEach(() => {
		mock.restore();
		Reflect.deleteProperty(globalThis, "window");
	});

	test("forwards the invitee's address as login_hint", async () => {
		const { url } = await startSignIn("bob@acme.test");
		expect(url.searchParams.get("login_hint")).toBe("bob@acme.test");
		expect(url.searchParams.get("redirect_uri")).toBe(REDIRECT);
	});

	test("sends no login_hint without an address", async () => {
		const { url } = await startSignIn(null);
		expect(url.searchParams.has("login_hint")).toBe(false);
	});

	test("reports a sign-in cancelled at the identity provider", async () => {
		const { client, url } = await startSignIn(null);
		const cancelled = callback({ error: "access_denied", state: url.searchParams.get("state") ?? "" });
		expect(await client.completeSignIn(cancelled).catch(String)).toContain("Sign-in was cancelled.");
	});

	test("reports other identity provider errors without their text", async () => {
		const { client, url } = await startSignIn(null);
		const failed = callback({
			error: "server_error",
			error_description: "Upstream down",
			state: url.searchParams.get("state") ?? "",
		});
		expect(await client.completeSignIn(failed).catch(String)).toContain(
			"The identity provider could not sign you in. Try again.",
		);
	});

	test("reports an expired sign-in when the callback is replayed", async () => {
		const { client, url } = await startSignIn(null);
		const cancelled = callback({ error: "access_denied", state: url.searchParams.get("state") ?? "" });
		expect(await client.completeSignIn(cancelled).catch(String)).toContain("Sign-in was cancelled.");
		expect(await client.completeSignIn(cancelled).catch(String)).toContain("This sign-in link has expired.");
	});
});
