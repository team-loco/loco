import { Button } from "@/components/ui/button";
import { LoginModal } from "@/components/LoginModal";
import {
	NavigationMenu,
	NavigationMenuContent,
	NavigationMenuItem,
	NavigationMenuLink,
	NavigationMenuList,
	NavigationMenuTrigger,
} from "@/components/ui/navigation-menu";
import { Cloud, Gauge, Network, Rocket, TrendingUp } from "lucide-react";
import { useAuth } from "@/auth/AuthProvider";
import { useState } from "react";

export function Splash() {
	const { isAuthenticated } = useAuth();
	const [loginModalOpen, setLoginModalOpen] = useState(false);

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
						<NavigationMenu className="hidden lg:flex">
							<NavigationMenuList>
								<NavigationMenuItem>
									<NavigationMenuTrigger className="bg-transparent hover:bg-transparent data-[state=open]:bg-transparent text-sm">
										Product
									</NavigationMenuTrigger>
									<NavigationMenuContent>
										<ul className="grid w-[320px] gap-2 p-4">
											<li>
												<NavigationMenuLink asChild>
													<a
														href="#features"
														className="block select-none space-y-1 rounded-md p-3 leading-none no-underline outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground"
													>
														<div className="text-sm font-medium">Features</div>
													</a>
												</NavigationMenuLink>
											</li>
											<li>
												<NavigationMenuLink asChild>
													<a
														href="#"
														className="block select-none space-y-1 rounded-md p-3 leading-none no-underline outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground"
													>
														<div className="text-sm font-medium">Pricing</div>
													</a>
												</NavigationMenuLink>
											</li>
										</ul>
									</NavigationMenuContent>
								</NavigationMenuItem>
								<NavigationMenuItem>
									<NavigationMenuTrigger className="bg-transparent hover:bg-transparent data-[state=open]:bg-transparent text-sm">
										Resources
									</NavigationMenuTrigger>
									<NavigationMenuContent>
										<ul className="grid w-[320px] gap-2 p-4">
											<li>
												<NavigationMenuLink asChild>
													<a
														href="https://github.com/team-loco/loco"
														target="_blank"
														className="block select-none space-y-1 rounded-md p-3 leading-none no-underline outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground"
													>
														<div className="text-sm font-medium">GitHub</div>
													</a>
												</NavigationMenuLink>
											</li>
											<li>
												<NavigationMenuLink asChild>
													<a
														href="#"
														className="block select-none space-y-1 rounded-md p-3 leading-none no-underline outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground"
													>
														<div className="text-sm font-medium">Docs</div>
													</a>
												</NavigationMenuLink>
											</li>
										</ul>
									</NavigationMenuContent>
								</NavigationMenuItem>
							</NavigationMenuList>
						</NavigationMenu>

						{/* Right Actions */}
						<div className="flex items-center gap-2 sm:gap-3 shrink-0">
							{isAuthenticated ? (
								<Button
									size="sm"
									className="bg-primary hover:bg-orange-600 text-primary-foreground h-9"
									asChild
								>
									<a href="/dashboard">Dashboard</a>
								</Button>
							) : (
								<Button
									size="sm"
									className="bg-primary hover:bg-orange-600 text-primary-foreground h-9"
									onClick={() => setLoginModalOpen(true)}
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
								onClick={() => setLoginModalOpen(true)}
							>
								Deploy Your First App
							</Button>
							<Button
								size="lg"
								className="bg-white/10 backdrop-blur text-white hover:bg-white/20 border border-white/30"
								asChild
							>
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
			<LoginModal
				open={loginModalOpen}
				onOpenChange={setLoginModalOpen}
			/>
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
