import { transport } from "@/auth/connect-transport";
import { Button } from "@/components/ui/button";
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
		<div className="min-h-screen bg-background flex">
			{/* Left Side - Branding & Marketing */}
			<div className="hidden lg:flex lg:w-1/2 flex-col items-center justify-center p-12 bg-gradient-to-br from-background to-muted">
				<div className="max-w-sm">
					<div className="mb-8">
						<div className="w-16 h-16 bg-destructive rounded-lg flex items-center justify-center text-white font-heading text-2xl mb-4">
							L
						</div>
						<h1 className="text-4xl font-heading text-foreground mb-2">Loco</h1>
						<p className="text-muted-foreground">Deploy & Scale</p>
					</div>

					<div className="space-y-6">
						<div>
							<h2 className="text-2xl font-heading text-foreground mb-3">
								Ship faster with Loco
							</h2>
							<p className="text-muted-foreground leading-relaxed">
								Deploy your applications in seconds. Loco handles the complexity
								of Kubernetes so you can focus on building great features.
							</p>
						</div>

						<div className="space-y-3">
							<div className="flex items-start gap-3">
								<div className="w-5 h-5 rounded-full bg-primary/20 flex items-center justify-center mt-0.5 flex-shrink-0">
									<div className="w-2 h-2 rounded-full bg-primary" />
								</div>
								<span className="text-sm text-foreground">
									One-command deployment
								</span>
							</div>
							<div className="flex items-start gap-3">
								<div className="w-5 h-5 rounded-full bg-primary/20 flex items-center justify-center mt-0.5 flex-shrink-0">
									<div className="w-2 h-2 rounded-full bg-primary" />
								</div>
								<span className="text-sm text-foreground">
									Automatic scaling
								</span>
							</div>
							<div className="flex items-start gap-3">
								<div className="w-5 h-5 rounded-full bg-primary/20 flex items-center justify-center mt-0.5 flex-shrink-0">
									<div className="w-2 h-2 rounded-full bg-primary" />
								</div>
								<span className="text-sm text-foreground">
									Zero downtime updates
								</span>
							</div>
						</div>
					</div>
				</div>
			</div>

			{/* Right Side - Auth Form */}
			<div className="w-full lg:w-1/2 flex flex-col items-center justify-center p-6 sm:p-12">
				<div className="w-full max-w-md">
					<div className="lg:hidden mb-8 text-center">
						<div className="w-12 h-12 bg-destructive rounded-lg flex items-center justify-center text-white font-heading text-lg mx-auto mb-3">
							L
						</div>
						<h1 className="text-2xl font-heading text-foreground">Loco</h1>
					</div>

					<div className="mb-8">
						<h2 className="text-2xl font-heading text-foreground mb-2">
							Get Started
						</h2>
						<p className="text-muted-foreground text-sm">
							Login using one of the following providers:
						</p>
					</div>

					{error && (
						<div className="bg-red-50 dark:bg-red-950 border border-red-200 dark:border-red-800 rounded-lg px-4 py-3 mb-6">
							<p className="text-sm text-red-800 dark:text-red-200">{error}</p>
						</div>
					)}

					<Button
						onClick={handleGithubLogin}
						disabled={isLoading}
						variant="default"
						className="w-[70%] h-11 flex items-center justify-center gap-2 mb-4 bg-amber-600"
					>
						<svg viewBox="0 0 1024 1024" fill="none" className="w-4 h-4">
							<path
								fillRule="evenodd"
								clipRule="evenodd"
								d="M8 0C3.58 0 0 3.58 0 8C0 11.54 2.29 14.53 5.47 15.59C5.87 15.66 6.02 15.42 6.02 15.21C6.02 15.02 6.01 14.39 6.01 13.72C4 14.09 3.48 13.23 3.32 12.78C3.23 12.55 2.84 11.84 2.5 11.65C2.22 11.5 1.82 11.13 2.49 11.12C3.12 11.11 3.57 11.7 3.72 11.94C4.44 13.15 5.59 12.81 6.05 12.6C6.12 12.08 6.33 11.73 6.56 11.53C4.78 11.33 2.92 10.64 2.92 7.58C2.92 6.71 3.23 5.99 3.74 5.43C3.66 5.23 3.38 4.41 3.82 3.31C3.82 3.31 4.49 3.1 6.02 4.13C6.66 3.95 7.34 3.86 8.02 3.86C8.7 3.86 9.38 3.95 10.02 4.13C11.55 3.09 12.22 3.31 12.22 3.31C12.66 4.41 12.38 5.23 12.3 5.43C12.81 5.99 13.12 6.7 13.12 7.58C13.12 10.65 11.25 11.33 9.47 11.53C9.76 11.78 10.01 12.26 10.01 13.01C10.01 14.08 10 14.94 10 15.21C10 15.42 10.15 15.67 10.55 15.59C13.71 14.53 16 11.53 16 8C16 3.58 12.42 0 8 0Z"
								transform="scale(64)"
								fill="currentColor"
							/>
						</svg>
						{isLoading ? "Redirecting..." : "Continue with GitHub"}
					</Button>
				</div>
			</div>
		</div>
	);
}
