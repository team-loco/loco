import { LoginModal } from "@/components/LoginModal";
import { Button } from "@/components/ui/button";

import { useAuth } from "@/auth/AuthProvider";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { Cloud, Gauge, Network, Rocket, TrendingUp } from "lucide-react";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router";

export function Splash() {
	const { isAuthenticated } = useAuth();
	const navigate = useNavigate();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const [loginModalOpen, setLoginModalOpen] = useState(false);

	const dashboardHref = useMemo(() => {
		if (isAuthenticated && activeOrgId && activeWorkspaceId) {
			return `/org/${activeOrgId}/wks/${activeWorkspaceId}`;
		}
		return "/dashboard";
	}, [isAuthenticated, activeOrgId, activeWorkspaceId]);

	return (
		<div className="min-h-screen flex flex-col bg-linear-to-b from-orange-50 to-amber-50 dark:from-orange-950/20 dark:to-amber-950/20 relative">
			<header className="sticky top-0 z-50 w-full bg-linear-to-b from-orange-50/50 to-amber-50/50 dark:from-orange-950/10 dark:to-amber-950/10 backdrop-blur-md">
				<div className="mx-auto max-w-[95%] py-2">
					<div className="flex items-center justify-between">
						{/* Logo */}
						<div className="flex items-center gap-3 shrink-0">
							<div className="w-8 h-8 bg-linear-to-br from-orange-500 to-orange-600 rounded-lg flex items-center justify-center">
								<Rocket className="w-5 h-5 text-white" />
							</div>
							<span className="text-lg font-bold text-foreground">Loco</span>
						</div>

						{/* Navigation */}
						<nav className="hidden lg:flex items-center gap-6">
							<a
								href="#features"
								className="text-sm font-medium text-foreground/80 hover:text-foreground transition-colors"
							>
								Features
							</a>
							<a
								href="#"
								className="text-sm font-medium text-foreground/80 hover:text-foreground transition-colors"
							>
								Pricing
							</a>
							<a
								href="#"
								className="text-sm font-medium text-foreground/80 hover:text-foreground transition-colors"
							>
								Docs
							</a>
							<a
								href="https://github.com/team-loco/loco"
								target="_blank"
								rel="noreferrer"
								className="text-sm font-medium text-foreground/80 hover:text-foreground transition-colors"
							>
								GitHub
							</a>
						</nav>

						{/* Right Actions */}
						<div className="flex items-center gap-2 sm:gap-3 shrink-0">
							{isAuthenticated ? (
								<Button
									size="sm"
									className="bg-primary hover:bg-orange-600 text-primary-foreground h-9"
									onClick={() => {
										void navigate(dashboardHref);
									}}
								>
									Dashboard
								</Button>
							) : (
								<Button
									size="sm"
									className="bg-primary hover:bg-orange-600 text-primary-foreground h-9"
									onClick={() => { setLoginModalOpen(true); }}
								>
									Get Started
								</Button>
							)}
						</div>
					</div>
				</div>
			</header>

			{/* Hero Section with Image */}
			<section className="relative overflow-hidden -mt-20 flex-1 flex flex-col">
				<div className="w-full flex-1 overflow-hidden relative flex flex-col bg-card dark:bg-card border-b-2 border-border shadow-sm dark:shadow-[inset_0_0_0_1.5px_rgba(255_255_255/0.05)]">
					{/* Background Image */}
					<div className="absolute inset-0">
						<img
							src="/landscape.jpg"
							alt="Loco deployment landscape"
							className="w-full h-full object-cover"
						/>
						{/* Overlay gradient */}
						<div className="absolute inset-0 bg-linear-to-b from-orange-950/20 via-orange-900/30 to-orange-950/35"></div>

						{/* Copyright overlay */}
						<div className="absolute bottom-4 left-0 right-0 text-center text-xs text-black/60 pointer-events-none z-20">
							&copy; {new Date().getFullYear()} Loco. All rights reserved.
						</div>
					</div>

					{/* Content */}
					<div className="relative z-10 md:pt-32 pt-24 md:pb-24 pb-12 px-4 lg:px-[159px] flex flex-col items-center text-center h-full justify-center">
						<h1 className="font-bold text-white tracking-tight leading-[1.12] text-[48px] sm:text-[64px] md:text-[80px]">
							Deploy with
							<br />
							<span className="bg-linear-to-r from-orange-200 to-orange-400 bg-clip-text text-transparent text-3xl">
								Simplicity
							</span>
						</h1>

						<p className="mt-6 text-white/90 text-[18px] sm:text-[20px] leading-7 max-w-[740px]">
							Loco simplifies application deployment. Run{" "}
							<code className="bg-black/40 backdrop-blur px-3 py-1 rounded font-mono text-sm text-orange-200">
								loco deploy
							</code>{" "}
							and we handle the rest—building, deploying, and scaling your apps
							on Kubernetes.
						</p>

						<div className="mt-8 flex flex-wrap items-center justify-center gap-3">
							<Button
								size="lg"
								className="bg-white text-orange-600 hover:bg-gray-100"
								onClick={() => { setLoginModalOpen(true); }}
							>
								Deploy Your First App
							</Button>
							<Button size="lg" variant="outline" asChild>
								<a href="#features">Learn More</a>
							</Button>
						</div>

						{/* Features Grid */}
						<div
							id="features"
							className="mt-20 grid md:grid-cols-2 lg:grid-cols-4 gap-4"
						>
							<FeatureCard
								icon={<Cloud className="w-6 h-6" />}
								title="Simple Deployments"
								description="One command to deploy all your apps"
							/>
							<FeatureCard
								icon={<TrendingUp className="w-6 h-6" />}
								title="Auto Scaling"
								description="Scale based on demand"
							/>
							<FeatureCard
								icon={<Network className="w-6 h-6" />}
								title="Secure, Private Networking"
								description="Envoy for HTTP/3, Cloudflare DNS Protection, and more"
							/>
							<FeatureCard
								icon={<Gauge className="w-6 h-6" />}
								title="Full Observability"
								description="Metrics, logs, and dashboards out of the box"
							/>
						</div>
					</div>

					{/* Bottom gradient fade - more gradual */}
					<div className="absolute bottom-0 left-0 right-0 h-64 bg-linear-to-t from-background to-transparent pointer-events-none"></div>
				</div>
			</section>

			{/* Login Modal */}
			<LoginModal open={loginModalOpen} onOpenChange={setLoginModalOpen} />
		</div>
	);
}

function FeatureCard({
	icon,
	title,
	description,
}: {
	icon: React.ReactNode;
	title: string;
	description: string;
}) {
	return (
		<div className="p-5 rounded-lg bg-orange-50 dark:bg-orange-950/30 group flex flex-col items-center text-center">
			<div className="w-12 h-12 flex items-center justify-center text-orange-600 mb-3">
				{icon}
			</div>
			<h3 className="font-semibold text-foreground mb-1 text-sm">{title}</h3>
			<p className="text-xs text-muted-foreground">{description}</p>
		</div>
	);
}
