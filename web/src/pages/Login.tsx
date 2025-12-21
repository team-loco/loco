import { transport } from "@/auth/connect-transport";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { OAuthService } from "@/gen/oauth/v1";
import { createClient } from "@connectrpc/connect";
import { useState } from "react";

export function Login() {
	const [error, setError] = useState<string | null>(() => {
		// Check if there's an error from OAuth callback
		const oauthError = sessionStorage.getItem("oauth_error");
		if (oauthError) {
			sessionStorage.removeItem("oauth_error");
			return oauthError;
		}
		return null;
	});
	const [isLoading, setIsLoading] = useState(false);

	const handleGithubLogin = async () => {
		try {
			setIsLoading(true);
			setError(null);

			// Call backend to get GitHub authorization URL
			const client = createClient(OAuthService, transport);
			const data = await client.getGithubAuthorizationURL({});

			const authUrl = data.authorizationUrl;

			if (!authUrl) {
				throw new Error("No authorization URL returned");
			}

			console.log("Opening GitHub OAuth URL:", authUrl);
			console.log(
				"Note: GitHub will echo back the state parameter in the callback URL"
			);

			// Redirect to GitHub in same window
			window.location.href = authUrl;
		} catch (err) {
			setError(err instanceof Error ? err.message : "Authentication failed");
			setIsLoading(false);
		}
	};

	return (
		<div className="min-h-screen bg-background flex items-center justify-center px-6">
			<Card className="max-w-md w-full">
				<CardContent className="p-8">
					<div className="mb-6 text-center">
						<div className="w-12 h-12 bg-destructive rounded-lg flex items-center justify-center text-white font-heading text-lg mx-auto mb-4">
							L
						</div>
						<h1 className="text-2xl font-heading text-foreground">Loco</h1>
					</div>

					<div className="space-y-4">
						{error && (
							<div className="bg-error-bg border border-error-border rounded px-3 py-2">
								<p className="text-xs text-error-text">{error}</p>
							</div>
						)}

						<Button
							onClick={handleGithubLogin}
							disabled={isLoading}
							className="w-full flex items-center justify-center gap-2"
						>
							{isLoading ? "Redirecting..." : "Continue with GitHub"}
						</Button>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
