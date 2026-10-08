import { createClient } from "@connectrpc/connect";

import { OAuthProvider, OAuthService } from "@gen/loco/oauth/v1/oauth_pb";

import { landingPath } from "@/auth/landing";

import { AuthStep, type AuthStepRun } from "./auth/AuthStep";

const signIn: AuthStepRun = async (transport, report) => {
	const params = new URLSearchParams(window.location.search);
	const providerError = params.get("error");
	if (providerError !== null) throw new Error(params.get("error_description") ?? "GitHub sign-in failed");
	const code = params.get("code");
	const state = params.get("state");
	if (code === null || state === null) throw new Error("The sign-in link is missing its code");

	const { userId } = await createClient(OAuthService, transport).exchangeOAuthCode({
		code,
		state,
		redirectUri: `${window.location.origin}/oauth/callback`,
		provider: OAuthProvider.GITHUB,
	});

	return await landingPath(transport, userId, report);
};

export function OAuthCallback() {
	return <AuthStep id="oauth" run={signIn} fallbackError="Sign-in failed" />;
}
