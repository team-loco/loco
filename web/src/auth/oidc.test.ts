import { afterEach, describe, expect, mock, spyOn, test } from "bun:test";

import { createAuthClient } from "./oidc";

const ISSUER = "https://idp.loco.test";
const REDIRECT = "https://app.loco.test/auth/callback";

async function authorizationURL(loginHint: string | null): Promise<URL> {
	spyOn(globalThis, "fetch").mockResolvedValue(
		Response.json({ issuer: ISSUER, authorization_endpoint: `${ISSUER}/authorize` }),
	);
	const assign = mock((_url: string) => undefined);
	Object.defineProperty(globalThis, "window", { value: { location: { assign } }, configurable: true });
	const client = await createAuthClient({ issuer: ISSUER, clientId: "loco-web", scopes: "openid email" });
	await client.signIn(REDIRECT, loginHint);
	const target = assign.mock.calls[0]?.[0];
	if (target === undefined) throw new Error("sign-in did not redirect");
	return new URL(target);
}

describe("sign-in redirect", () => {
	afterEach(() => {
		mock.restore();
		Reflect.deleteProperty(globalThis, "window");
	});

	test("forwards the invitee's address as login_hint", async () => {
		const url = await authorizationURL("bob@acme.test");
		expect(url.searchParams.get("login_hint")).toBe("bob@acme.test");
		expect(url.searchParams.get("redirect_uri")).toBe(REDIRECT);
	});

	test("sends no login_hint without an address", async () => {
		const url = await authorizationURL(null);
		expect(url.searchParams.has("login_hint")).toBe(false);
	});
});
