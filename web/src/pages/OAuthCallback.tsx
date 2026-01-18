import { exchangeOAuthCode, OAuthProvider } from "@/gen/loco/oauth/v1";
import { listUserOrgs } from "@/gen/loco/org/v1";
import { getErrorMessage } from "@/lib/error-handler";
import { useQuery } from "@connectrpc/connect-query";
import { useEffect, useMemo } from "react";
import { useNavigate } from "react-router";
import Loader from "@/assets/loader.svg?react";

export function OAuthCallback() {
	const navigate = useNavigate();

	// Get authorization code and state from URL (GitHub sends both back)
	const params = useMemo(() => new URLSearchParams(window.location.search), []);
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
		code && state ? { code: code || "", state: state || "", redirectUri: window.location.origin + "/oauth/callback", provider: OAuthProvider.GITHUB } : undefined,
		{
			enabled: !!code && !!state,
		}
	);

	// After exchange, check if user has orgs
	// Use the user ID from the exchange response
	const {
		data: orgsRes,
		isLoading: orgsLoading,
		error: orgsError,
	} = useQuery(
		listUserOrgs,
		exchangeRes?.userId ? { userId: BigInt(exchangeRes.userId) } : undefined,
		{ enabled: !isLoading && !!exchangeRes }
	);

	useEffect(() => {
		if (error) {
			const errorMsg = errorDescription || "OAuth error";
			console.error("OAuthCallback: OAuth error:", errorMsg);
			sessionStorage.setItem("oauth_error", errorMsg);
			navigate("/login");
			return;
		}

		if (!code && !state && !error) {
			// No OAuth params, just show loading
			return;
		}

		if (queryError) {
			const errorMsg = getErrorMessage(
				queryError,
				"Failed to exchange authorization code"
			);
			console.error("OAuthCallback: Exchange error:", errorMsg);
			sessionStorage.setItem("oauth_error", errorMsg);
			navigate("/login");
			return;
		}

		if (orgsError) {
			const errorMsg = getErrorMessage(
				orgsError,
				"Failed to load user organizations"
			);
			console.error("OAuthCallback: Orgs error:", errorMsg);
			sessionStorage.setItem("oauth_error", errorMsg);
			navigate("/login");
			return;
		}

		if (!isLoading && code && state && !queryError && exchangeRes) {
			console.log("OAuthCallback: Response received");
			console.log("Token is set as HTTP-only cookie by backend");

			// Clear any previous OAuth errors on successful login
			sessionStorage.removeItem("oauth_error");
		}

		// After orgs are loaded, check if user has any orgs
		if (!orgsLoading && orgsRes) {
			const hasOrgs = (orgsRes.orgs ?? []).length > 0;

			if (hasOrgs) {
				console.log("OAuthCallback: User has orgs, navigating to dashboard");
				navigate("/dashboard");
			} else {
				console.log(
					"OAuthCallback: User has no orgs, navigating to onboarding"
				);
				navigate("/onboarding");
			}
		}
	}, [
		code,
		state,
		error,
		errorDescription,
		queryError,
		isLoading,
		exchangeRes,
		orgsLoading,
		orgsRes,
		orgsError,
		navigate,
	]);

	return (
		<div className="min-h-screen bg-background flex items-center justify-center px-6">
			<div className="flex flex-col gap-2 items-center">
				<Loader className="w-16 h-16" />
				<p className="text-foreground font-base">Authenticating...</p>
			</div>
		</div>
	);
}
