import { exchangeGithubCode } from "@/gen/oauth/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { useQuery } from "@connectrpc/connect-query";
import { useEffect, useMemo } from "react";
import { useNavigate } from "react-router";

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
		exchangeGithubCode,
		code && state
			? {
					code,
					state,
					redirectUri: window.location.origin + "/oauth/callback",
			  }
			: undefined,
		{ enabled: !!(code && state) }
	);

	// After exchange, check if user has orgs
	const { data: orgsRes, isLoading: orgsLoading } = useQuery(
		getCurrentUserOrgs,
		{},
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
			const errorMsg =
				queryError.message || "Failed to exchange authorization code";
			console.error("OAuthCallback: Exchange error:", errorMsg);
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
		navigate,
	]);

	return (
		<div className="min-h-screen bg-background flex items-center justify-center px-6">
			<div className="inline-flex gap-2 items-center mb-4">
				<div className="w-4 h-4 bg-main rounded-full animate-pulse"></div>
				<p className="text-foreground font-base">Authenticating...</p>
			</div>
		</div>
	);
}
