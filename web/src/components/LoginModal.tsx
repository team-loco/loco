import { createClient } from "@connectrpc/connect";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";

import { OAuthProvider, OAuthService } from "@gen/loco/oauth/v1/oauth_pb";

import { useAuth } from "@/auth/AuthProvider";
import type { AuthAdapter, LoginMethod } from "@/auth/adapters/types";
import { transport } from "@/auth/connect-transport";
import { signInRedirectURL } from "@/auth/adapter";
import { Button } from "@/components/design/Button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { LocoLogo } from "@/components/design/LocoLogo";
import { SoonTag } from "@/components/design/SoonTag";
import { getErrorMessage } from "@/lib/error-handler";

interface LoginModalProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	initialError?: string | null | undefined;
}

function GitHubMark({ className }: { className?: string }) {
	return (
		<svg viewBox="0 0 16 16" fill="currentColor" className={className} aria-hidden="true">
			<path
				fillRule="evenodd"
				clipRule="evenodd"
				d="M8 0C3.58 0 0 3.58 0 8C0 11.54 2.29 14.53 5.47 15.59C5.87 15.66 6.02 15.42 6.02 15.21C6.02 15.02 6.01 14.39 6.01 13.72C4 14.09 3.48 13.23 3.32 12.78C3.23 12.55 2.84 11.84 2.5 11.65C2.22 11.5 1.82 11.13 2.49 11.12C3.12 11.11 3.57 11.7 3.72 11.94C4.44 13.15 5.59 12.81 6.05 12.6C6.12 12.08 6.33 11.73 6.56 11.53C4.78 11.33 2.92 10.64 2.92 7.58C2.92 6.71 3.23 5.99 3.74 5.43C3.66 5.23 3.38 4.41 3.82 3.31C3.82 3.31 4.49 3.1 6.02 4.13C6.66 3.95 7.34 3.86 8.02 3.86C8.7 3.86 9.38 3.95 10.02 4.13C11.55 3.09 12.22 3.31 12.22 3.31C12.66 4.41 12.38 5.23 12.3 5.43C12.81 5.99 13.12 6.7 13.12 7.58C13.12 10.65 11.25 11.33 9.47 11.53C9.76 11.78 10.01 12.26 10.01 13.01C10.01 14.08 10 14.94 10 15.21C10 15.42 10.15 15.67 10.55 15.59C13.71 14.53 16 11.53 16 8C16 3.58 12.42 0 8 0Z"
			/>
		</svg>
	);
}

function GoogleMark({ className }: { className?: string }) {
	return (
		<svg viewBox="0 0 24 24" className={className} aria-hidden="true">
			<path
				d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
				fill="#4285F4"
			/>
			<path
				d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
				fill="#34A853"
			/>
			<path
				d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"
				fill="#FBBC05"
			/>
			<path
				d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"
				fill="#EA4335"
			/>
		</svg>
	);
}

const PROVIDER_MARKS: Record<string, (props: { className?: string }) => ReactNode> = {
	github: GitHubMark,
	google: GoogleMark,
};

function emailDomain(email: string): string | null {
	const at = email.lastIndexOf("@");
	if (at < 1 || at === email.length - 1) return null;
	return email.slice(at + 1).toLowerCase();
}

type Pending = { kind: "oauth"; provider: string } | { kind: "sso" } | { kind: "redirect" } | null;

function ProviderLogin({ adapter, onError }: { adapter: AuthAdapter; onError: (message: string | null) => void }) {
	const [pending, setPending] = useState<Pending>(null);
	const [email, setEmail] = useState("");
	const methods = useQuery({
		queryKey: ["auth", "login-methods", adapter.kind],
		queryFn: async (): Promise<LoginMethod[]> => await adapter.loginMethods(),
		staleTime: 5 * 60 * 1000,
		gcTime: 0,
	});

	const attempt = async (next: Pending, action: () => Promise<void>) => {
		setPending(next);
		onError(null);
		try {
			await action();
		} catch (err) {
			onError(getErrorMessage(err, "Sign-in failed"));
			setPending(null);
		}
	};

	const submitSSO = (event: FormEvent) => {
		event.preventDefault();
		const domain = emailDomain(email.trim());
		if (domain === null) {
			onError("Enter your work email address");
			return;
		}
		void attempt({ kind: "sso" }, async () => {
			await adapter.signInWithSSO(domain, signInRedirectURL());
		});
	};

	if (methods.isPending) {
		return (
			<div className="flex justify-center py-6 text-fg3">
				<Loader2 className="size-5 animate-spin" />
			</div>
		);
	}
	if (methods.error !== null) {
		return (
			<div role="alert" className="rounded-sm bg-bad-bg px-3 py-2 text-center text-sm text-bad-fg">
				{getErrorMessage(methods.error, "Could not load sign-in options")}
			</div>
		);
	}

	const list = methods.data;
	const oauth = list.filter((m) => m.kind === "oauth");
	const redirect = list.find((m) => m.kind === "redirect");
	const hasSSO = list.some((m) => m.kind === "sso");
	const busy = pending !== null;

	return (
		<div className="flex flex-col gap-2">
			{redirect !== undefined && (
				<Button
					variant="inverted"
					size="lg"
					className="w-full"
					disabled={busy}
					onClick={() => {
						void attempt({ kind: "redirect" }, async () => {
							await adapter.signInWithRedirect(signInRedirectURL());
						});
					}}
				>
					{pending?.kind === "redirect" && <Loader2 className="size-4 animate-spin" />}
					{redirect.label}
				</Button>
			)}
			{oauth.map((method, i) => {
				const Mark = PROVIDER_MARKS[method.provider];
				const loading = pending?.kind === "oauth" && pending.provider === method.provider;
				return (
					<Button
						key={method.provider}
						variant={i === 0 ? "inverted" : "outline"}
						size="lg"
						className="w-full"
						disabled={busy}
						onClick={() => {
							void attempt({ kind: "oauth", provider: method.provider }, async () => {
								await adapter.signInWithOAuth(method.provider, signInRedirectURL());
							});
						}}
					>
						{loading ? <Loader2 className="size-4 animate-spin" /> : Mark !== undefined && <Mark className="size-4" />}
						{loading ? "Redirecting…" : `Continue with ${method.label}`}
					</Button>
				);
			})}
			{hasSSO && (
				<>
					{(oauth.length > 0 || redirect !== undefined) && (
						<div className="my-2 flex items-center gap-3 text-sm text-fg4">
							<span className="h-px flex-1 bg-line" />
							or
							<span className="h-px flex-1 bg-line" />
						</div>
					)}
					<form className="flex flex-col gap-2" onSubmit={submitSSO}>
						<Input
							type="email"
							required
							autoComplete="email"
							aria-label="Work email"
							placeholder="you@company.com"
							value={email}
							onChange={(event) => {
								setEmail(event.target.value);
							}}
							className="h-[34px]"
						/>
						<Button type="submit" variant="outline" size="lg" className="w-full" disabled={busy || email.trim() === ""}>
							{pending?.kind === "sso" && <Loader2 className="size-4 animate-spin" />}
							Continue with SSO
						</Button>
					</form>
				</>
			)}
		</div>
	);
}

function LegacyGithubLogin({ onError }: { onError: (message: string | null) => void }) {
	const [isGithubLoading, setIsGithubLoading] = useState(false);

	const handleGithubLogin = async () => {
		try {
			setIsGithubLoading(true);
			onError(null);

			const client = createClient(OAuthService, transport);
			const data = await client.getOAuthAuthorizationURL({
				provider: OAuthProvider.GITHUB,
			});
			const authUrl = data.authorizationUrl;

			if (!authUrl) {
				throw new Error("No authorization URL returned");
			}

			const redirectUri = `${window.location.origin}/oauth/callback`;
			const url = new URL(authUrl);
			url.searchParams.set("redirect_uri", redirectUri);

			window.location.href = url.toString();
		} catch (err) {
			onError(getErrorMessage(err, "Authentication failed"));
			setIsGithubLoading(false);
		}
	};

	return (
		<div className="flex flex-col gap-2">
			<Button
				variant="inverted"
				size="lg"
				className="w-full"
				onClick={() => {
					void handleGithubLogin();
				}}
				disabled={isGithubLoading}
			>
				{isGithubLoading ? <Loader2 className="size-4 animate-spin" /> : <GitHubMark className="size-4" />}
				{isGithubLoading ? "Redirecting..." : "Continue with GitHub"}
			</Button>

			<Button variant="outline" size="lg" className="w-full cursor-not-allowed text-fg4" disabled>
				<GoogleMark className="size-4 opacity-60" />
				Continue with Google
				<SoonTag />
			</Button>
		</div>
	);
}

export function LoginModal({ open, onOpenChange, initialError }: LoginModalProps) {
	const { adapter } = useAuth();
	const [error, setError] = useState<string | null>(initialError ?? null);

	const handleOpenChange = (newOpen: boolean) => {
		if (!newOpen) {
			setError(null);
		}
		onOpenChange(newOpen);
	};

	return (
		<Dialog open={open} onOpenChange={handleOpenChange}>
			<DialogContent className="w-[380px] gap-5 px-8 pt-9 pb-6">
				<div className="flex flex-col items-center gap-3 text-center">
					<LocoLogo motion="once" drawKey="sign-in" className="mb-1 w-24" />
					<DialogTitle className="text-xl font-semibold">Let&apos;s get started</DialogTitle>
					<DialogDescription className="text-fg3">Sign in to your account</DialogDescription>
				</div>

				{error !== null && (
					<div role="alert" className="rounded-sm bg-bad-bg px-3 py-2 text-center text-sm text-bad-fg">
						{error}
					</div>
				)}

				{adapter === null ? (
					<LegacyGithubLogin onError={setError} />
				) : (
					<ProviderLogin adapter={adapter} onError={setError} />
				)}

				<p className="m-0 text-center text-sm text-fg3">
					<a href="#" className="underline hover:text-foreground">
						Terms of Service
					</a>
				</p>
			</DialogContent>
		</Dialog>
	);
}
