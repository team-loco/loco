import { useQuery } from "@connectrpc/connect-query";
import { useEffect } from "react";
import { useNavigate } from "react-router";

import { exchangeOAuthCode } from "@gen/loco/oauth/v1/oauth-OAuthService_connectquery";
import { OAuthProvider } from "@gen/loco/oauth/v1/oauth_pb";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";

import { AuthStatusScreen } from "@/components/AuthStatusScreen";
import { getErrorMessage } from "@/lib/error-handler";
import { OAUTH_ERROR_KEY, removeStorage, writeStorage } from "@/lib/storage";

export function OAuthCallback() {
	const navigate = useNavigate();

	const params = new URLSearchParams(window.location.search);
	const code = params.get("code");
	const state = params.get("state");
	const error = params.get("error");
	const errorDescription = params.get("error_description");

	const {
		data: exchangeRes,
		isLoading,
		error: queryError,
	} = useQuery(
		exchangeOAuthCode,
		code && state
			? {
					code,
					state,
					redirectUri: `${window.location.origin}/oauth/callback`,
					provider: OAuthProvider.GITHUB,
				}
			: undefined,
		{
			enabled: !!code && !!state,
		},
	);

	const {
		data: orgsRes,
		isLoading: orgsLoading,
		error: orgsError,
	} = useQuery(listUserOrgs, exchangeRes?.userId ? { userId: exchangeRes.userId } : undefined, {
		enabled: !isLoading && !!exchangeRes,
	});

	useEffect(() => {
		const fail = (message: string) => {
			writeStorage(OAUTH_ERROR_KEY, message, "session");
			void navigate("/login");
		};

		if (error) {
			const errorMsg = errorDescription ?? "OAuth error";
			console.error("OAuthCallback: OAuth error:", errorMsg);
			fail(errorMsg);
			return;
		}

		if (!code && !state) {
			return;
		}

		if (queryError) {
			const errorMsg = getErrorMessage(queryError, "Failed to exchange authorization code");
			console.error("OAuthCallback: Exchange error:", errorMsg);
			fail(errorMsg);
			return;
		}

		if (orgsError) {
			const errorMsg = getErrorMessage(orgsError, "Failed to load user organizations");
			console.error("OAuthCallback: Orgs error:", errorMsg);
			fail(errorMsg);
			return;
		}

		if (!isLoading && code && state && exchangeRes) {
			removeStorage(OAUTH_ERROR_KEY, "session");
		}

		if (!orgsLoading && orgsRes) {
			void navigate(orgsRes.orgs.length > 0 ? "/dashboard" : "/onboarding");
		}
	}, [code, state, error, errorDescription, queryError, isLoading, exchangeRes, orgsLoading, orgsRes, orgsError, navigate]);

	return <AuthStatusScreen title="Authenticating…" />;
}
