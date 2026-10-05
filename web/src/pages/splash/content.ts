import { BarChart3, GitBranch, Globe, Lock, Rocket, TrendingUp } from "lucide-react";

export const GITHUB_URL = "https://github.com/team-loco/loco";
export const GITHUB_ISSUES_URL = "https://github.com/team-loco/loco/issues";

export const LOCO_TOML_EXAMPLE = `\
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

export const FEATURES = [
	{
		icon: Rocket,
		title: "One Command to Deploy",
		desc: "Bring a Dockerfile, run loco deploy. Loco builds your image, pushes it, and orchestrates it on Kubernetes with zero-downtime rolling updates.",
	},
	{
		icon: Lock,
		title: "HTTPS by Default",
		desc: "Automatic SSL certificate management via Let's Encrypt and cert-manager. Your app is served over HTTPS with no extra config.",
	},
	{
		icon: Globe,
		title: "Global Traffic Routing",
		desc: "Envoy Gateway serves HTTP/3 traffic with Cloudflare DNS protection and intelligent routing. Multi-region deployments available.",
	},
	{
		icon: BarChart3,
		title: "Metrics & Logs",
		desc: "Built-in OpenTelemetry pipeline with ClickHouse storage and Grafana dashboards. Scrape Prometheus metrics or ship structured logs—zero config.",
	},
	{
		icon: TrendingUp,
		title: "Auto Scaling",
		desc: "Scale up automatically under load and back down when things quiet. Configure CPU and memory targets per region in your loco.toml.",
	},
	{
		icon: GitBranch,
		title: "Preview Environments",
		desc: "Spin up isolated environments for any deployment, each with its own URL. Test before it hits production.",
	},
] as const;

export const OPEN_STANDARDS = ["Kubernetes", "OpenTelemetry", "Envoy", "Cilium"] as const;

export const FOOTER_COLUMNS = [
	{
		title: "Product",
		links: [
			{ label: "Features", href: "#features" },
			{ label: "Pricing", href: "#" },
			{ label: "Changelog", href: "#" },
			{ label: "Roadmap", href: "#" },
		],
	},
	{
		title: "Resources",
		links: [
			{ label: "Documentation", href: "#" },
			{ label: "API Reference", href: "https://buf.build/team-loco/loco" },
			{ label: "CLI Guide", href: "#" },
			{ label: "GitHub Issues", href: GITHUB_ISSUES_URL },
		],
	},
	{
		title: "Open Source",
		links: [
			{ label: "GitHub", href: GITHUB_URL },
			{ label: "Issues", href: GITHUB_ISSUES_URL },
			{ label: "License", href: "https://github.com/team-loco/loco/blob/main/LICENSE" },
			{ label: "Contributing", href: "#" },
		],
	},
] as const;
