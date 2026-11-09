import { Activity, KeyRound, Lock, RefreshCw, ShieldCheck, Zap, type LucideIcon } from "lucide-react";

import { Section, SectionTitle } from "./primitives";

const FEATURES: readonly { icon: LucideIcon; title: string; body: string; tech: string }[] = [
	{
		icon: Lock,
		title: "HTTPS by default",
		body: "Services on the platform domain are served over HTTPS, with certificates renewed before they expire.",
		tech: "cert-manager · Let's Encrypt",
	},
	{
		icon: Zap,
		title: "HTTP/3 at the edge",
		body: "Envoy terminates TLS and routes each hostname to its service.",
		tech: "Envoy Gateway · HTTP/3",
	},
	{
		icon: RefreshCw,
		title: "Rolling deploys",
		body: "Replicas are replaced a quarter at a time and take traffic once their health check passes. Roll back from the dashboard.",
		tech: "25% surge · 25% max unavailable",
	},
	{
		icon: ShieldCheck,
		title: "Private services",
		body: "Leave out the domain and a service only accepts connections from inside its workspace.",
		tech: "Kubernetes NetworkPolicy",
	},
	{
		icon: Activity,
		title: "Logs, traces and metrics",
		body: "Write logs to stdout. Every replica gets an OpenTelemetry endpoint for traces.",
		tech: "OpenTelemetry · ClickHouse",
	},
	{
		icon: KeyRound,
		title: "Scoped tokens",
		body: "Give CI access to one workspace or a few resources, with an expiry.",
		tech: "organization · workspace · resource",
	},
];

export function Features() {
	return (
		<Section id="features">
			<SectionTitle>Everything a service needs.</SectionTitle>
			<div className="grid gap-px overflow-hidden rounded-overlay border border-line bg-line min-[900px]:grid-cols-3">
				{FEATURES.map(({ icon: Icon, title, body, tech }) => (
					<div
						key={title}
						className="flex flex-col gap-1.5 bg-background p-6 transition-colors duration-150 hover:bg-panel"
					>
						<Icon aria-hidden="true" className="mb-2 size-[18px] text-brand" />
						<span className="text-[15px] font-medium">{title}</span>
						<span className="text-[13.5px] text-pretty text-fg3">{body}</span>
						<span className="mt-auto pt-3 font-mono text-[11.5px] text-fg3">{tech}</span>
					</div>
				))}
			</div>
		</Section>
	);
}
