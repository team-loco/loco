import { useState } from "react";
import { Navigate, useLocation } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { LoginModal } from "@/components/LoginModal";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";

import { GITHUB_URL } from "./splash/content";
import { GitHubIcon } from "./splash/GitHubIcon";
import { HeroTerminal } from "./splash/HeroTerminal";
import { ConfigSection, CtaSection, FeaturesSection, OpenSourceSection } from "./splash/sections";
import { SplashFooter } from "./splash/SplashFooter";
import { LocoLogo } from "@/components/design/LocoLogo";

const NAV_LINKS = [
	{ label: "Features", href: "#features" },
	{ label: "Open Source", href: "#oss" },
	{ label: "Docs", href: "#" },
] as const;

function oauthErrorFrom(state: unknown): string | null {
	if (typeof state !== "object" || state === null || !("oauthError" in state)) return null;
	return typeof state.oauthError === "string" ? state.oauthError : null;
}

export function Splash() {
	const { isAuthenticated } = useAuth();
	const location = useLocation();
	const oauthError = oauthErrorFrom(location.state);
	const [loginModalOpen, setLoginModalOpen] = useState(oauthError !== null);

	if (isAuthenticated) {
		return <Navigate to="/dashboard" replace />;
	}

	const openLogin = () => {
		setLoginModalOpen(true);
	};

	return (
		<div className="min-h-screen overflow-x-hidden bg-background text-foreground">
			<nav className="sticky top-0 z-50 bg-background">
				<div className="mx-auto flex h-14 max-w-[1200px] items-center justify-between gap-4 px-4 md:px-8">
					<a href="/" className="flex items-center" aria-label="Loco home">
						<LocoLogo className="w-[72px]" />
					</a>
					<div className="hidden items-center gap-6 lg:flex">
						{NAV_LINKS.map((link) => (
							<a key={link.label} href={link.href} className="text-fg2 transition-colors hover:text-foreground">
								{link.label}
							</a>
						))}
						<a
							href={GITHUB_URL}
							target="_blank"
							rel="noreferrer"
							className="flex items-center gap-1.5 text-fg2 transition-colors hover:text-foreground"
						>
							<GitHubIcon className="size-3.5" />
							GitHub
						</a>
					</div>
					<Button size="sm" variant="outline" onClick={openLogin}>
						Sign in
					</Button>
				</div>
			</nav>

			<section className="px-4 md:px-8">
				<div className="mx-auto grid max-w-[1200px] items-center gap-12 py-20 lg:grid-cols-2 lg:py-24">
					<div className="flex flex-col items-start">
						<Badge tone="outline" className="mb-6">
							Fully Open Source
						</Badge>
						<h1 className="m-0 mb-5 text-[48px] leading-[1.05] font-semibold tracking-[-0.03em] sm:text-[56px]">
							Deploy with <span className="text-primary">Confidence</span>
						</h1>
						<p className="m-0 mb-8 max-w-lg text-lg leading-relaxed text-fg2">
							Modern infrastructure for a modern world. Bring a Dockerfile, run{" "}
							<code className="rounded-xs border border-line bg-bg3 px-1.5 py-px font-mono text-sm text-foreground">
								loco deploy
							</code>
							, and we handle the rest.
						</p>
						<div className="flex flex-wrap gap-3">
							<Button size="xl" onClick={openLogin}>
								Deploy Your First App
							</Button>
							<Button
								variant="outline"
								size="xl"
								nativeButton={false}
								render={<a href={GITHUB_URL} target="_blank" rel="noreferrer" />}
							>
								<GitHubIcon className="size-4" />
								View on GitHub
							</Button>
						</div>
					</div>
					<HeroTerminal />
				</div>
			</section>

			<FeaturesSection />
			<ConfigSection />
			<OpenSourceSection />
			<CtaSection onStart={openLogin} />
			<SplashFooter />

			<LoginModal open={loginModalOpen} onOpenChange={setLoginModalOpen} initialError={oauthError} />
		</div>
	);
}
