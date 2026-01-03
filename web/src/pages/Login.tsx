import { transport } from "@/auth/connect-transport";
import { Button } from "@/components/ui/button";
import { OAuthService } from "@/gen/oauth/v1";
import { createClient } from "@connectrpc/connect";
import { useState } from "react";
import { useAuth } from "@/auth/AuthProvider";
import { Navigate } from "react-router";
import Loader from "@/assets/loader.svg?react";

export function Login() {
	const { isAuthenticated } = useAuth();
	const [error, setError] = useState<string | null>(() => {
		// Check if there's an error from OAuth callback
		const oauthError = sessionStorage.getItem("oauth_error");
		if (oauthError) {
			sessionStorage.removeItem("oauth_error");
			return oauthError;
		}
		return null;
	});
	const [isGithubLoading, setIsGithubLoading] = useState(false);

	const handleGithubLogin = async () => {
		try {
			setIsGithubLoading(true);
			setError(null);

			const client = createClient(OAuthService, transport);
			const data = await client.getGithubAuthorizationURL({});
			const authUrl = data.authorizationUrl;

			if (!authUrl) {
				throw new Error("No authorization URL returned");
			}

			const redirectUri = `${window.location.origin}/oauth/callback`;
			const url = new URL(authUrl);
			url.searchParams.set("redirect_uri", redirectUri);

			window.location.href = url.toString();
		} catch (err) {
			setError(err instanceof Error ? err.message : "Authentication failed");
			setIsGithubLoading(false);
		}
	};

	if (isAuthenticated) {
		return <Navigate to="/dashboard" />;
	}
	return (
		<div className="grid grid-cols-1 lg:grid-cols-2 min-h-screen bg-white">
			{/* Left Side - Form */}
			<div className="relative min-h-screen bg-white">
				{/* Mobile background image */}
				<div className="lg:hidden absolute inset-0 overflow-hidden rounded-2xl">
					<img
						src="gradient.svg"
						alt=""
						className="w-full h-full object-cover scale-110"
					/>
				</div>

				{/* Logo */}
				<div className="absolute top-6 lg:top-8 z-10 ml-4">
					<div className="w-10 h-10 bg-destructive rounded-lg flex items-center justify-center text-white font-heading text-lg">
						L
					</div>
				</div>

				{/* Form Container */}
				<div className="flex items-center justify-center min-h-screen p-6 lg:p-8 relative z-10">
					<div className="w-full max-w-md bg-white rounded-2xl p-6 lg:p-8 lg:bg-transparent lg:rounded-none space-y-6 shadow-lg lg:shadow-none">
						<div className="space-y-2 text-center">
							<h1 className="text-2xl lg:text-4xl font-heading text-black leading-tight">
								Welcome to Loco
							</h1>
							<p className="text-sm text-gray-600">
								Deploy your applications in seconds
							</p>
						</div>

						{error && (
							<div className="bg-red-50 border border-red-200 rounded-lg px-4 py-3">
								<p className="text-sm text-red-800">{error}</p>
							</div>
						)}

						<Button
							onClick={handleGithubLogin}
							disabled={isGithubLoading}
							variant="outline"
							className="w-full h-10 flex items-center justify-center gap-2"
						>
							{isGithubLoading ? (
								<Loader className="w-4 h-4" />
							) : (
								<svg viewBox="0 0 1024 1024" fill="none" className="w-4 h-4">
									<path
										fillRule="evenodd"
										clipRule="evenodd"
										d="M8 0C3.58 0 0 3.58 0 8C0 11.54 2.29 14.53 5.47 15.59C5.87 15.66 6.02 15.42 6.02 15.21C6.02 15.02 6.01 14.39 6.01 13.72C4 14.09 3.48 13.23 3.32 12.78C3.23 12.55 2.84 11.84 2.5 11.65C2.22 11.5 1.82 11.13 2.49 11.12C3.12 11.11 3.57 11.7 3.72 11.94C4.44 13.15 5.59 12.81 6.05 12.6C6.12 12.08 6.33 11.73 6.56 11.53C4.78 11.33 2.92 10.64 2.92 7.58C2.92 6.71 3.23 5.99 3.74 5.43C3.66 5.23 3.38 4.41 3.82 3.31C3.82 3.31 4.49 3.1 6.02 4.13C6.66 3.95 7.34 3.86 8.02 3.86C8.7 3.86 9.38 3.95 10.02 4.13C11.55 3.09 12.22 3.31 12.22 3.31C12.66 4.41 12.38 5.23 12.3 5.43C12.81 5.99 13.12 6.7 13.12 7.58C13.12 10.65 11.25 11.33 9.47 11.53C9.76 11.78 10.01 12.26 10.01 13.01C10.01 14.08 10 14.94 10 15.21C10 15.42 10.15 15.67 10.55 15.59C13.71 14.53 16 11.53 16 8C16 3.58 12.42 0 8 0Z"
										transform="scale(64)"
										fill="currentColor"
									/>
								</svg>
							)}
							{isGithubLoading ? "Redirecting..." : "Continue with GitHub"}
						</Button>
					</div>
				</div>

				{/* Footer */}
				<div className="absolute bottom-6 left-6 right-6 lg:bottom-8 lg:left-8 lg:right-8 z-20">
					<p className="text-center text-xs text-muted-foreground">
						By signing in, you agree to our{" "}
						<a href="#" className="underline hover:text-foreground">
							Terms of Service
						</a>
					</p>
				</div>
			</div>

			{/* Right Side - Gradient Background with Quote (Desktop only) */}
			<div className="hidden lg:block relative min-h-screen p-4">
				<div className="h-full w-full bg-none rounded-2xl overflow-hidden">
					<div className="relative h-full w-full">
						{/* Animated gradient background */}
						<img
							src="/gradient.svg"
							alt="gradient background"
							className="absolute inset-0 h-full w-full object-cover scale-110"
						/>

						{/* Elevational Quote Card */}
						<div className="absolute inset-0 flex items-center justify-center py-6 px-12 xl:px-16 2xl:px-20 pointer-events-none">
							<div className="w-full max-w-3xl">
								<div className="bg-linear-to-br from-white/80 to-white/60 backdrop-blur-md rounded-xl xl:rounded-2xl 2xl:rounded-3xl p-6 xl:p-8 2xl:p-10 border border-white/40 shadow-2xl">
									<div className="flex items-center gap-4">
										<div className="flex-1 flex items-center gap-3">
											<blockquote className="text-gray-800 text-lg xl:text-xl 2xl:text-2xl leading-relaxed font-light">
												"Ship your ideas faster than ever before. Loco handles
												Kubernetes complexity so you can focus on what matters."
											</blockquote>
										</div>
									</div>
									<p className="text-sm text-gray-700 mt-4 font-medium">
										— The Loco Team
									</p>
								</div>
							</div>
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}
