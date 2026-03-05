import { useAuth } from "@/auth/AuthProvider";
import { LoginModal } from "@/components/LoginModal";
import { Button } from "@/components/ui/button";
import { CodeBlock } from "@/components/ui/code-block";
import {
	AnimatedSpan,
	Terminal,
	TypingAnimation,
} from "@/components/ui/terminal";
import { useOrgWorkspace } from "@/context/ContextProvider";
import {
	BarChart3,
	FileCode2,
	GitBranch,
	Globe,
	Lock,
	MessageSquare,
	Rocket,
	TrendingUp,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";

// ─── Data ────────────────────────────────────────────────────────────────────

const LOCO_TOML_EXAMPLE = `\
[Metadata]
Name = "my-api"
Region = "us-east-1"

[Build]
DockerfilePath = "Dockerfile"

[Routing]
Port = 8000
PathPrefix = "/"

[DomainConfig]
Hostname = "my-api"

[RegionConfig."us-east-1"]
CPU = "100m"
Memory = "256Mi"

[RegionConfig."us-east-1".Replicas]
Min = 1
Max = 3
`;

const FEATURES = [
	{
		icon: Rocket,
		color: "#C7654F",
		title: "One Command to Deploy",
		desc: "Bring a Dockerfile, run loco deploy. Loco builds your image, pushes it, and orchestrates it on Kubernetes with zero-downtime rolling updates.",
	},
	{
		icon: Lock,
		color: "#6B8E7F",
		title: "HTTPS by Default",
		desc: "Automatic SSL certificate management via Let's Encrypt and cert-manager. Your app is served over HTTPS with no extra config.",
	},
	{
		icon: Globe,
		color: "#A88F6F",
		title: "Global Traffic Routing",
		desc: "Envoy Gateway serves HTTP/3 traffic with Cloudflare DNS protection and intelligent routing. Multi-region deployments available.",
	},
	{
		icon: BarChart3,
		color: "#6B8AAA",
		title: "Metrics & Logs",
		desc: "Built-in OpenTelemetry pipeline with ClickHouse storage and Grafana dashboards. Scrape Prometheus metrics or ship structured logs—zero config.",
	},
	{
		icon: TrendingUp,
		color: "#6B8E7F",
		title: "Auto Scaling",
		desc: "Scale up automatically under load and back down when things quiet. Configure CPU and memory targets per region in your loco.toml.",
	},
	{
		icon: GitBranch,
		color: "#C7654F",
		title: "Preview Environments",
		desc: "Spin up isolated environments for any deployment, each with its own URL. Test before it hits production.",
	},
] as const;

// ─── Component ────────────────────────────────────────────────────────────────

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

	// Redirect authenticated users straight to the dashboard
	useEffect(() => {
		if (isAuthenticated) {
			void navigate(dashboardHref);
		}
	}, [isAuthenticated, dashboardHref, navigate]);

	return (
		<div className="min-h-screen bg-[#FDFCFA] text-[#1C1917] overflow-x-hidden">
			{/* ── Nav ─────────────────────────────────────────────────────── */}
			<nav className="fixed top-0 left-0 right-0 z-50 bg-white/30 backdrop-blur-xl border-b border-white/20">
				<div className="max-w-7xl mx-auto px-6 lg:px-12 py-2 flex items-center justify-between">
					<img src="/logo.png" alt="Loco" className="h-8 w-8 rounded-lg" />

					<div className="hidden lg:flex items-center gap-8">
						<a
							href="#features"
							className="text-sm font-medium text-[#57534E] hover:text-[#1C1917] transition-colors"
						>
							Features
						</a>
						<a
							href="#oss"
							className="text-sm font-medium text-[#57534E] hover:text-[#1C1917] transition-colors"
						>
							Open Source
						</a>
						<a
							href="#"
							className="text-sm font-medium text-[#57534E] hover:text-[#1C1917] transition-colors"
						>
							Docs
						</a>
						<a
							href="https://github.com/team-loco/loco"
							target="_blank"
							rel="noreferrer"
							className="text-sm font-medium text-[#57534E] hover:text-[#1C1917] transition-colors flex items-center gap-1.5"
						>
							<GitHubIcon className="w-4 h-4" />
							GitHub
						</a>
					</div>

					<div>
						{isAuthenticated && (
							<Button
								size="sm"
								onClick={() => {
									void navigate(dashboardHref);
								}}
							>
								Dashboard
							</Button>
						)}
					</div>
				</div>
			</nav>

			{/* ── Hero ────────────────────────────────────────────────────── */}
			<section className="relative min-h-screen flex items-center overflow-hidden pt-20">
				{/* Background image */}
				<div className="absolute inset-0">
					<img
						src="/landscape.webp"
						alt=""
						className="w-full h-full object-cover"
					/>
					{/* <div className="absolute inset-0 bg-gradient-to-b from-black/40 via-black/30 to-black/50" /> */}
				</div>

				<div className="relative z-10 max-w-7xl mx-auto px-6 lg:px-12 w-full grid lg:grid-cols-2 gap-16 items-center py-16 lg:py-14">
					{/* Text */}
					<div>
						<div className="inline-flex items-center bg-white/20 backdrop-blur-sm border border-white/30 rounded-full px-2 py-1 text-sm font-medium text-white/90 mb-8">
							Fully Open Source
						</div>

						<h1 className="text-6xl sm:text-6xl lg:text-[72px] font-bold leading-[1.08] tracking-tight mb-6">
							Deploy with{" "}
							<span className="text-[#C7654F] text-3xl">Confidence</span>
						</h1>

						<p className="text-lg sm:text-xl text-white/90 leading-relaxed mb-10 max-w-lg drop-shadow-sm">
							Modern infrastructure for a modern world. Bring a Dockerfile, run{" "}
							<code className="bg-black/30 backdrop-blur border border-white/20 px-2 py-0.5 rounded-md font-mono text-sm text-orange-300">
								loco deploy
							</code>
							, and we handle the rest.
						</p>

						<div className="flex flex-wrap gap-4">
							<button
								onClick={() => {
									setLoginModalOpen(true);
								}}
								className="px-7 py-3.5 bg-[#C7654F] text-white border-2 border-[#1C1917] rounded-sm font-bold text-base shadow-[4px_4px_0px_#1C1917] hover:shadow-[2px_2px_0px_#1C1917] hover:translate-x-0.5 hover:translate-y-0.5 transition-all cursor-pointer"
							>
								Deploy Your First App
							</button>
							<a
								href="https://github.com/team-loco/loco"
								target="_blank"
								rel="noreferrer"
								className="px-7 py-3.5 bg-white text-[#1C1917] border-2 border-[#1C1917] rounded-sm font-bold text-base shadow-[4px_4px_0px_#1C1917] hover:shadow-[2px_2px_0px_#1C1917] hover:translate-x-0.5 hover:translate-y-0.5 transition-all flex items-center gap-2"
							>
								<GitHubIcon className="w-4 h-4" />
								View on GitHub
							</a>
						</div>
					</div>

					{/* Terminal */}
					<Terminal
						className="bg-[#0D2118] border-white/10 max-h-none max-w-none shadow-xl font-mono"
						headerClassName="bg-[#091811] border-white/5"
					>
						{/* $ loco init my-api */}
						<AnimatedSpan delay={0} className="flex gap-2">
							<span style={{ color: "#6B8E7F" }}>$</span>
							<TypingAnimation delay={200} className="text-[#F8D8C6]">
								{"loco init my-api"}
							</TypingAnimation>
						</AnimatedSpan>
						<AnimatedSpan delay={1400} className="text-[#94A8B0]">
							✓ Created loco.toml in working directory.
						</AnimatedSpan>
						{/* spacer */}
						<AnimatedSpan delay={1900} className="select-none opacity-0">
							{" "}
						</AnimatedSpan>
						{/* $ loco deploy */}
						<AnimatedSpan delay={2100} className="flex gap-2">
							<span style={{ color: "#6B8E7F" }}>$</span>
							<TypingAnimation delay={2300} className="text-[#F8D8C6]">
								{"loco deploy"}
							</TypingAnimation>
						</AnimatedSpan>
						<AnimatedSpan delay={3100} className="text-[#94A8B0]">
							→ Building image from Dockerfile...
						</AnimatedSpan>
						<AnimatedSpan delay={3500} className="text-[#94A8B0]">
							→ Pushing to registry...
						</AnimatedSpan>
						<AnimatedSpan delay={3900} className="text-[#94A8B0]">
							→ Creating Kubernetes resources...
						</AnimatedSpan>
						<AnimatedSpan delay={4300} className="text-[#94A8B0]">
							→ Provisioning SSL certificate...
						</AnimatedSpan>
						<AnimatedSpan delay={4800} className="text-[#CCDE68]">
							✓ Deployment successful
						</AnimatedSpan>
						<AnimatedSpan delay={5100} className="text-[#94A8B0]">
							{"  "}https://my-api.onloco.app
						</AnimatedSpan>
					</Terminal>
				</div>
			</section>

			{/* ── Features ─────────────────────────────────────────────────── */}
			<section
				id="features"
				className="py-16 lg:py-14 px-6 lg:px-12 bg-[#F7F5F2]"
			>
				<div className="max-w-7xl mx-auto">
					<div className="text-center mb-12">
						<p className="text-xs font-bold uppercase tracking-widest text-[#C7654F] mb-3">
							Features
						</p>
						<h2 className="text-4xl lg:text-5xl font-bold tracking-tight mb-4">
							Everything You Need
						</h2>
						<p className="text-lg text-[#57534E] max-w-lg mx-auto">
							Ship with confidence. Modern infrastructure without the
							complexity.
						</p>
					</div>

					<div className="grid md:grid-cols-2 lg:grid-cols-3 gap-6">
						{FEATURES.map(({ icon: Icon, color, title, desc }) => (
							<div
								key={title}
								className="bg-white border border-[#E7E5E0] rounded-xl p-7 hover:shadow-md hover:-translate-y-0.5 transition-all duration-200"
							>
								<Icon className="w-6 h-6 mb-4" style={{ color }} />
								<h3 className="text-base font-semibold mb-2">{title}</h3>
								<p className="text-sm text-[#57534E] leading-relaxed">{desc}</p>
							</div>
						))}
					</div>
				</div>
			</section>

			{/* ── Config Example ────────────────────────────────────────────── */}
			<section className="py-16 lg:py-14 px-6 lg:px-12 bg-[#FDFCFA]">
				<div className="max-w-5xl mx-auto grid lg:grid-cols-2 gap-16 items-center">
					<div>
						<p className="text-xs font-bold uppercase tracking-widest text-[#C7654F] mb-3">
							Simple Configuration
						</p>
						<h2 className="text-4xl font-bold tracking-tight mb-5">
							One File.
							<br />
							Full Control.
						</h2>
						<p className="text-[#57534E] text-lg leading-relaxed mb-4">
							A{" "}
							<code className="bg-[#F7F5F2] border border-[#E7E5E0] px-2 py-0.5 rounded font-mono text-sm">
								loco.toml
							</code>{" "}
							is all you need. No Kubernetes manifests, no Helm charts to
							debug—just clean, readable config with sensible defaults.
						</p>
						<p className="text-[#57534E] text-lg leading-relaxed">
							Run{" "}
							<code className="bg-[#F7F5F2] border border-[#E7E5E0] px-2 py-0.5 rounded font-mono text-sm">
								loco init
							</code>{" "}
							to generate a starter file, then customise from there. You can
							also configure everything through the UI.
						</p>
					</div>

					<CodeBlock filename="loco.toml" language="toml">
						{LOCO_TOML_EXAMPLE}
					</CodeBlock>
				</div>
			</section>

			{/* ── Open Source ───────────────────────────────────────────────── */}
			<section id="oss" className="py-16 lg:py-14 px-6 lg:px-12 bg-[#F7F5F2]">
				<div className="max-w-7xl mx-auto">
					<div className="text-center mb-12">
						<p className="text-xs font-bold uppercase tracking-widest text-[#C7654F] mb-3">
							Open Source
						</p>
						<h2 className="text-4xl lg:text-5xl font-bold tracking-tight mb-4">
							Built in the Open. Always.
						</h2>
						<p className="text-lg text-[#57534E] max-w-xl mx-auto leading-relaxed">
							Loco is fully open source and always will be. Read the code,
							contribute, or self-host—it's yours to use.
						</p>
					</div>

					<div className="grid md:grid-cols-3 gap-6">
						<div className="bg-white border border-[#E7E5E0] rounded-xl p-7">
							<GitHubIcon className="w-6 h-6 mb-4 text-[#1C1917]" />
							<h3 className="text-base font-semibold mb-2">
								Full Source on GitHub
							</h3>
							<p className="text-sm text-[#57534E] leading-relaxed mb-4">
								Every line of Loco's code is public. Browse the source, fork it,
								run it yourself. No hidden pieces, no proprietary black boxes.
							</p>
							<a
								href="https://github.com/team-loco/loco"
								target="_blank"
								rel="noreferrer"
								className="text-sm font-semibold text-[#C7654F] hover:text-[#B55942] transition-colors"
							>
								team-loco/loco →
							</a>
						</div>

						<div className="bg-white border border-[#E7E5E0] rounded-xl p-7">
							<FileCode2
								className="w-6 h-6 mb-4"
								style={{ color: "#6B8E7F" }}
							/>
							<h3 className="text-base font-semibold mb-2">
								Built on Open Standards
							</h3>
							<p className="text-sm text-[#57534E] leading-relaxed mb-4">
								Kubernetes, OpenTelemetry, Envoy, Cilium—we build on OSS
								foundations you already know and trust.
							</p>
							<div className="flex flex-wrap gap-2">
								{["Kubernetes", "OpenTelemetry", "Envoy", "Cilium"].map(
									(tag) => (
										<span
											key={tag}
											className="text-xs bg-[#F7F5F2] border border-[#E7E5E0] px-2.5 py-1 rounded-full text-[#57534E]"
										>
											{tag}
										</span>
									),
								)}
							</div>
						</div>

						<div className="bg-white border border-[#E7E5E0] rounded-xl p-7">
							<MessageSquare
								className="w-6 h-6 mb-4"
								style={{ color: "#C7654F" }}
							/>
							<h3 className="text-base font-semibold mb-2">
								We Want Your Feedback
							</h3>
							<p className="text-sm text-[#57534E] leading-relaxed mb-4">
								Found a bug? Have a feature idea? Open a GitHub issue. We read
								every one and take them seriously.
							</p>
							<a
								href="https://github.com/team-loco/loco/issues"
								target="_blank"
								rel="noreferrer"
								className="text-sm font-semibold text-[#C7654F] hover:text-[#B55942] transition-colors"
							>
								Open an issue →
							</a>
						</div>
					</div>
				</div>
			</section>

			{/* ── CTA ──────────────────────────────────────────────────────── */}
			<section
				className="relative py-20 px-6 lg:px-12 overflow-hidden"
				style={{
					background: "linear-gradient(135deg, #C7654F 0%, #A85442 100%)",
				}}
			>
				<div className="absolute top-0 right-0 w-[600px] h-[600px] rounded-full bg-white/10 blur-3xl -translate-y-1/2 translate-x-1/4" />
				<div className="absolute bottom-0 left-0 w-[400px] h-[400px] rounded-full bg-white/5 blur-3xl translate-y-1/2 -translate-x-1/4" />
				<div className="max-w-3xl mx-auto text-center relative z-10">
					<h2 className="text-4xl sm:text-5xl font-bold text-white mb-5 tracking-tight">
						Ready to Deploy?
					</h2>
					<p className="text-xl text-white/90 mb-8 leading-relaxed max-w-xl mx-auto">
						Join developers shipping with confidence. Get started in minutes—no
						credit card required.
					</p>
					<div className="flex flex-wrap gap-4 justify-center">
						<button
							onClick={() => {
								setLoginModalOpen(true);
							}}
							className="px-8 py-4 bg-white text-[#C7654F] rounded-lg font-semibold text-base hover:bg-white/95 transition-all hover:-translate-y-0.5 shadow-md cursor-pointer"
						>
							Start Deploying Free
						</button>
						<a
							href="https://github.com/team-loco/loco"
							target="_blank"
							rel="noreferrer"
							className="px-8 py-4 bg-transparent text-white border-2 border-white/70 rounded-lg font-semibold text-base hover:bg-white/10 transition-all flex items-center gap-2"
						>
							<GitHubIcon className="w-5 h-5" />
							Star on GitHub
						</a>
					</div>
				</div>
			</section>

			{/* ── Footer ───────────────────────────────────────────────────── */}
			<footer className="bg-[#FDFCFA] border-t border-[#E7E5E0] px-6 lg:px-12 pt-10 pb-6">
				<div className="max-w-7xl mx-auto grid sm:grid-cols-2 lg:grid-cols-4 gap-10 mb-8">
					<div className="sm:col-span-2 lg:col-span-1">
						<div className="flex items-center gap-2.5 mb-3">
							<img src="/logo.png" alt="Loco" className="h-7 w-7 rounded-lg" />
							<span className="text-lg font-bold tracking-tight">LOCO</span>
						</div>
						<p className="text-sm text-[#57534E] leading-relaxed">
							Modern infrastructure for a modern world.
						</p>
					</div>

					<div>
						<h4 className="text-xs font-semibold uppercase tracking-wider text-[#1C1917] mb-3">
							Product
						</h4>
						<ul className="space-y-2.5">
							{[
								{ label: "Features", href: "#features" },
								{ label: "Pricing", href: "#" },
								{ label: "Changelog", href: "#" },
								{ label: "Roadmap", href: "#" },
							].map(({ label, href }) => (
								<li key={label}>
									<a
										href={href}
										className="text-sm text-[#57534E] hover:text-[#C7654F] transition-colors"
									>
										{label}
									</a>
								</li>
							))}
						</ul>
					</div>

					<div>
						<h4 className="text-xs font-semibold uppercase tracking-wider text-[#1C1917] mb-3">
							Resources
						</h4>
						<ul className="space-y-2.5">
							{[
								{ label: "Documentation", href: "#" },
								{
									label: "API Reference",
									href: "https://buf.build/team-loco/loco",
								},
								{ label: "CLI Guide", href: "#" },
								{
									label: "GitHub Issues",
									href: "https://github.com/team-loco/loco/issues",
								},
							].map(({ label, href }) => (
								<li key={label}>
									<a
										href={href}
										target={href.startsWith("http") ? "_blank" : undefined}
										rel={href.startsWith("http") ? "noreferrer" : undefined}
										className="text-sm text-[#57534E] hover:text-[#C7654F] transition-colors"
									>
										{label}
									</a>
								</li>
							))}
						</ul>
					</div>

					<div>
						<h4 className="text-xs font-semibold uppercase tracking-wider text-[#1C1917] mb-3">
							Open Source
						</h4>
						<ul className="space-y-2.5">
							{[
								{ label: "GitHub", href: "https://github.com/team-loco/loco" },
								{
									label: "Issues",
									href: "https://github.com/team-loco/loco/issues",
								},
								{
									label: "License",
									href: "https://github.com/team-loco/loco/blob/main/LICENSE",
								},
								{ label: "Contributing", href: "#" },
							].map(({ label, href }) => (
								<li key={label}>
									<a
										href={href}
										target={href.startsWith("http") ? "_blank" : undefined}
										rel={href.startsWith("http") ? "noreferrer" : undefined}
										className="text-sm text-[#57534E] hover:text-[#C7654F] transition-colors"
									>
										{label}
									</a>
								</li>
							))}
						</ul>
					</div>
				</div>

				<div className="max-w-7xl mx-auto pt-6 border-t border-[#E7E5E0] flex flex-col sm:flex-row items-center justify-between gap-3 text-sm text-[#78716C]">
					<span>© {new Date().getFullYear()} Loco. All rights reserved.</span>
					<span>Built with ❤️ for developers by developers</span>
				</div>
			</footer>

			<LoginModal open={loginModalOpen} onOpenChange={setLoginModalOpen} />
		</div>
	);
}

function GitHubIcon({ className }: { className?: string }) {
	return (
		<svg
			viewBox="0 0 24 24"
			fill="currentColor"
			className={className}
			aria-hidden="true"
		>
			<path d="M12 0C5.374 0 0 5.373 0 12c0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23A11.509 11.509 0 0112 5.803c1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576C20.566 21.797 24 17.3 24 12c0-6.627-5.373-12-12-12z" />
		</svg>
	);
}
