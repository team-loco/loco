import { Activity, KeyRound, Lock, RefreshCw, TrendingUp, Zap } from "lucide-react";

import { CornerMarks, Frame, SectionHeading } from "./primitives";

const FEATURES = [
	{
		icon: Lock,
		title: "HTTPS by default",
		body: "Every service gets a certificate on deploy, renewed before it expires.",
		tech: "cert-manager · Let's Encrypt",
	},
	{
		icon: Zap,
		title: "HTTP/3 at the edge",
		body: "Envoy terminates TLS, speaks QUIC and routes by host and path.",
		tech: "Envoy Gateway API",
	},
	{
		icon: RefreshCw,
		title: "Zero-downtime rollouts",
		body: "New replicas must pass health checks before old ones drain. One-click rollback.",
		tech: "rolling update · surge 1",
	},
	{
		icon: TrendingUp,
		title: "Autoscaling",
		body: "Set a replica range and a CPU target per region.",
		tech: "HPA",
	},
	{
		icon: Activity,
		title: "Logs and metrics",
		body: "Collected from every replica, searchable and charted in the dashboard.",
		tech: "OpenTelemetry · ClickHouse",
	},
	{
		icon: KeyRound,
		title: "Granular tokens",
		body: "Give CI write on one service and nothing else. Expire in 30 days or less.",
		tech: "read · write · admin per entity",
	},
] as const;

export function Features() {
	return (
		<section className="border-t border-line bg-bg2">
			<Frame className="flex flex-col gap-12 py-28">
				<CornerMarks />
				<SectionHeading index="02" eyebrow="Included" title="The platform work, already done." />
				<div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,320px),1fr))] border-t border-l border-line">
					{FEATURES.map(({ icon: Icon, title, body, tech }) => (
						<div key={title} className="flex flex-col gap-2.5 border-r border-b border-line bg-background p-7">
							<span className="flex items-center gap-2.5">
								<Icon size={18} strokeWidth={1.6} className="text-primary" />
								<span className="font-semibold">{title}</span>
							</span>
							<span className="text-[14.5px] text-pretty text-fg2">{body}</span>
							<span className="font-mono text-sm text-fg4">{tech}</span>
						</div>
					))}
				</div>
			</Frame>
		</section>
	);
}
