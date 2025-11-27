import { Card, CardContent } from "@/components/ui/card";
import { useEffect, useMemo } from "react";
import { useNavigate } from "react-router";
import { useQuery } from "@connectrpc/connect-query";
import { exchangeGithubCode } from "@/gen/oauth/v1";

export function OAuthCallback() {
	const navigate = useNavigate();

	// Get authorization code and state from URL (GitHub sends both back)
	const params = useMemo(() => new URLSearchParams(window.location.search), []);
	const code = params.get("code");
	const state = params.get("state");
	const error = params.get("error");
	const errorDescription = params.get("error_description");

	const { data: exchangeRes, isLoading, error: queryError } = useQuery(
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
			const errorMsg = queryError.message || "Failed to exchange authorization code";
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

			console.log("OAuthCallback: Success, navigating to home");
			// Navigate to home - auth state will update automatically
			navigate("/");
		}
	}, [code, state, error, errorDescription, queryError, isLoading, exchangeRes, navigate]);

	return (
		<div className="min-h-screen bg-background flex items-center justify-center px-6">
			<Card className="max-w-md w-full">
				<CardContent className="p-8 text-center">
					<div className="inline-flex gap-2 items-center mb-4">
						<div className="w-4 h-4 bg-main rounded-full animate-pulse"></div>
						<p className="text-foreground font-base">Authenticating...</p>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
