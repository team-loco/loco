import Loader from "@/assets/loader.svg?react";
import {
	DeploymentWizard,
	type DeploymentWizardValues,
} from "@/components/DeploymentWizard";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Slider } from "@/components/ui/slider";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { createDeployment } from "@/gen/loco/deployment/v1";
import {
	DeploymentPhase,
	type Deployment,
} from "@/gen/loco/deployment/v1/deployment_pb";
import { updateResourceDomain } from "@/gen/loco/domain/v1";
import type { ResourceDomain } from "@/gen/loco/domain/v1/domain_pb";
import {
	deleteResource,
	scaleResource,
	updateResource,
} from "@/gen/loco/resource/v1";
import { useResourceDetails } from "@/hooks/useResourceDetails";
import { useStreamEvents } from "@/hooks/useStreamEvents";
import { getStatusLabel } from "@/lib/app-status";
import { getServiceSpec } from "@/lib/deployment-utils";
import { getErrorMessage } from "@/lib/error-handler";
import { subscribeToEvents } from "@/lib/events";
import { cn } from "@/lib/utils";
import { useMutation } from "@connectrpc/connect-query";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";

const STATUS_CFG: Record<
	string,
	{ dot: string; color: string; bg: string; label: string }
> = {
	healthy: {
		dot: "#4a7c59",
		color: "#3a6b4a",
		bg: "#eaf2ed",
		label: "Healthy",
	},
	deploying: {
		dot: "#5b7ec0",
		color: "#3a5298",
		bg: "#e8edf8",
		label: "Deploying",
	},
	degraded: {
		dot: "#d4870a",
		color: "#9c6b1e",
		bg: "#fdf3e3",
		label: "Degraded",
	},
	failed: {
		dot: "#c0392b",
		color: "#8b2e2e",
		bg: "#fdeaea",
		label: "Unavailable",
	},
	suspended: {
		dot: "#b0a090",
		color: "#7a6a58",
		bg: "#f0ece6",
		label: "Suspended",
	},
	pending: {
		dot: "#b0a090",
		color: "#7a6a58",
		bg: "#f0ece6",
		label: "Pending",
	},
};

const PHASE_CFG: Record<
	DeploymentPhase,
	{ label: string; bg: string; color: string }
> = {
	[DeploymentPhase.UNSPECIFIED]: {
		label: "Unknown",
		bg: "#ede7dd",
		color: "#7a6a58",
	},
	[DeploymentPhase.PENDING]: {
		label: "Pending",
		bg: "#ede7dd",
		color: "#7a6a58",
	},
	[DeploymentPhase.DEPLOYING]: {
		label: "Deploying",
		bg: "#e8edf8",
		color: "#3a5298",
	},
	[DeploymentPhase.RUNNING]: {
		label: "Running",
		bg: "#eaf2ed",
		color: "#3a6b4a",
	},
	[DeploymentPhase.SUCCEEDED]: {
		label: "Succeeded",
		bg: "#eaf2ed",
		color: "#3a6b4a",
	},
	[DeploymentPhase.FAILED]: {
		label: "Failed",
		bg: "#fdeaea",
		color: "#8b2e2e",
	},
	[DeploymentPhase.CANCELED]: {
		label: "Canceled",
		bg: "#f0ece6",
		color: "#8a7a68",
	},
};

const NODE_STYLE: Record<string, { border: string; bg: string; text: string }> =
	{
		self: { border: "#c4956a", bg: "#fdf6ee", text: "#3d2a14" },
		service: { border: "#c0b8ac", bg: "#faf7f2", text: "#4a3c30" },
		gateway: { border: "#8fa8c8", bg: "#f0f4fa", text: "#2a3e5c" },
		database: { border: "#8fbfa8", bg: "#f0f8f4", text: "#1e4a38" },
		cache: { border: "#b8a0c8", bg: "#f6f0fa", text: "#3c2460" },
		external: { border: "#c0b8ac", bg: "#f5f2ec", text: "#6b5d4f" },
	};

// ─── Helpers ──────────────────────────────────────────────────────────────────

function statusKeyFromLabel(label: string): string {
	if (label === "running") return "healthy";
	if (label === "unavailable") return "failed";
	return label;
}

function relativeTime(timestamp: unknown): string {
	if (!timestamp) return "—";
	try {
		let ms: number;
		if (
			typeof timestamp === "object" &&
			timestamp !== null &&
			"seconds" in timestamp
		) {
			ms = Number((timestamp as Record<string, unknown>).seconds) * 1000;
		} else if (typeof timestamp === "number") {
			ms = timestamp;
		} else {
			return "—";
		}
		const diff = Date.now() - ms;
		const mins = Math.floor(diff / 60_000);
		const hrs = Math.floor(diff / 3_600_000);
		const days = Math.floor(diff / 86_400_000);
		if (mins < 1) return "Just now";
		if (mins < 60) return `${mins}m ago`;
		if (hrs < 24) return `${hrs}h ago`;
		if (days === 1) return "Yesterday";
		return `${days}d ago`;
	} catch {
		return "—";
	}
}

function shortId(id: string): string {
	if (!id || id.length <= 16) return id;
	return `${id.slice(0, 13)}…`;
}

function deploymentImage(dep: Deployment): string {
	const svc = getServiceSpec(dep);
	let img = svc?.build?.image ?? "—";
	img = img.replace("registry.gitlab.com/locomotive-group/", "");
	return img;
}

// Parse "500m" → 500, "2" → 2000
function parseCpuMilli(cpu: string): number {
	if (!cpu) return 0;
	if (cpu.endsWith("m")) return parseInt(cpu, 10);
	return Math.round(parseFloat(cpu) * 1000);
}

// Parse "512Mi" → 512, "1Gi" → 1024, "1.5Gi" → 1536
function parseMemMi(mem: string): number {
	if (!mem) return 0;
	if (mem.endsWith("Gi")) return Math.round(parseFloat(mem) * 1024);
	if (mem.endsWith("Mi")) return Math.round(parseFloat(mem));
	return Math.round(parseFloat(mem));
}

// Mock a usage pct based on health status + deterministic seed
function mockUsagePct(statusKey: string, seed: number): number {
	const base = [23, 31, 17, 41, 29, 37, 13, 43][seed % 8];
	if (statusKey === "degraded") return 72 + (base % 18);
	if (statusKey === "failed") return 88 + (base % 10);
	return 28 + (base % 38);
}

// ─── Architecture Diagram ─────────────────────────────────────────────────────

interface ArchNode {
	id: string;
	label: string;
	type: string;
	x: number;
	y: number;
	replicas?: number;
}
interface ArchEdge {
	from: string;
	to: string;
	label: string;
}

function ArchDiagram({ resourceName }: { resourceName: string }) {
	const [hovered, setHovered] = useState<string | null>(null);

	const NW = 124,
		NH = 46;

	const nodes: ArchNode[] = [
		{ id: "gateway", label: "api-gateway", type: "gateway", x: 240, y: 36 },
		{
			id: "self",
			label: resourceName,
			type: "self",
			x: 240,
			y: 148,
			replicas: 3,
		},
		{ id: "postgres", label: "postgres-db", type: "database", x: 430, y: 96 },
		{ id: "redis", label: "redis-cache", type: "cache", x: 430, y: 200 },
		{ id: "ext", label: "External API", type: "external", x: 50, y: 148 },
	];

	const edges: ArchEdge[] = [
		{ from: "gateway", to: "self", label: "routes traffic" },
		{ from: "ext", to: "self", label: "calls inbound" },
		{ from: "self", to: "postgres", label: "reads / writes" },
		{ from: "self", to: "redis", label: "session cache" },
	];

	const activeEdges = hovered
		? edges.filter((e) => e.from === hovered || e.to === hovered)
		: [];

	const edgePath = (edge: ArchEdge) => {
		const f = nodes.find((n) => n.id === edge.from);
		const t = nodes.find((n) => n.id === edge.to);
		if (!f || !t) return null;
		const fx = f.x + NW / 2,
			fy = f.y + NH / 2;
		const tx = t.x + NW / 2,
			ty = t.y + NH / 2;
		const dx = tx - fx,
			dy = ty - fy;
		let x1: number, y1: number, x2: number, y2: number;
		if (Math.abs(dx) > Math.abs(dy)) {
			x1 = dx > 0 ? f.x + NW : f.x;
			y1 = fy;
			x2 = dx > 0 ? t.x : t.x + NW;
			y2 = ty;
		} else {
			x1 = fx;
			y1 = dy > 0 ? f.y + NH : f.y;
			x2 = tx;
			y2 = dy > 0 ? t.y : t.y + NH;
		}
		const mx = (x1 + x2) / 2,
			my = (y1 + y2) / 2;
		const d =
			Math.abs(dx) > Math.abs(dy)
				? `M${x1} ${y1} C${mx} ${y1},${mx} ${y2},${x2} ${y2}`
				: `M${x1} ${y1} C${x1} ${my},${x2} ${my},${x2} ${y2}`;
		return { d, mx, my };
	};

	return (
		<div>
			<svg viewBox="0 0 600 280" className="w-full block">
				<defs>
					<marker
						id="arr"
						markerWidth="7"
						markerHeight="6"
						refX="7"
						refY="3"
						orient="auto"
					>
						<polygon points="0 0,7 3,0 6" fill="#cec4b8" />
					</marker>
					<marker
						id="arrA"
						markerWidth="7"
						markerHeight="6"
						refX="7"
						refY="3"
						orient="auto"
					>
						<polygon points="0 0,7 3,0 6" fill="#c4956a" />
					</marker>
				</defs>

				{edges.map((edge, i) => {
					const p = edgePath(edge);
					if (!p) return null;
					const active =
						hovered && (edge.from === hovered || edge.to === hovered);
					return (
						<g key={i}>
							<path
								d={p.d}
								fill="none"
								stroke={active ? "#c4956a" : "#ddd5c8"}
								strokeWidth={active ? 1.5 : 1}
								strokeDasharray={active ? "none" : "5 4"}
								markerEnd={active ? "url(#arrA)" : "url(#arr)"}
								style={{ transition: "stroke 0.18s, stroke-width 0.18s" }}
							/>
							{active && (
								<text
									x={p.mx}
									y={p.my - 7}
									textAnchor="middle"
									fontSize="9.5"
									fill="#b07840"
									fontFamily="'DM Mono',monospace"
								>
									{edge.label}
								</text>
							)}
						</g>
					);
				})}

				{nodes.map((node) => {
					const s = NODE_STYLE[node.type] ?? NODE_STYLE.service;
					const isActive =
						hovered === node.id ||
						activeEdges.some((e) => e.from === node.id || e.to === node.id);
					const isSelf = node.type === "self";
					const subtitles: Record<string, string> = {
						self: `${node.replicas ?? 1} replicas`,
						gateway: "ingress",
						database: "postgresql",
						cache: "redis",
						external: "external",
						service: "service",
					};
					return (
						<g
							key={node.id}
							transform={`translate(${node.x},${node.y})`}
							className="cursor-pointer"
							onMouseEnter={() => {
								setHovered(node.id);
							}}
							onMouseLeave={() => {
								setHovered(null);
							}}
						>
							<rect
								x={0}
								y={0}
								width={NW}
								height={NH}
								rx={9}
								fill={s.bg}
								stroke={isActive || isSelf ? s.border : "#e2d8cc"}
								strokeWidth={isSelf ? 2.5 : isActive ? 1.5 : 1}
								style={{
									transition: "all 0.15s",
									filter: isActive
										? "drop-shadow(0 3px 10px rgba(0,0,0,0.09))"
										: "none",
								}}
							/>
							<text
								x={NW / 2}
								y={18}
								textAnchor="middle"
								fontSize="11.5"
								fontWeight={isSelf ? "600" : "500"}
								fontFamily="'DM Mono',monospace"
								fill={s.text}
							>
								{node.label}
							</text>
							<text
								x={NW / 2}
								y={33}
								textAnchor="middle"
								fontSize="9"
								fill="#a89880"
							>
								{subtitles[node.type] ?? ""}
							</text>
							{node.type !== "external" && (
								<circle cx={NW - 9} cy={9} r={4} fill="#4a7c59" />
							)}
						</g>
					);
				})}
			</svg>

			<div className="px-5 py-3.5 border-t border-[#ede7dd] flex gap-3.5 flex-wrap items-center">
				{(
					[
						["self", "This service"],
						["gateway", "Gateway"],
						["database", "Database"],
						["cache", "Cache"],
						["external", "External"],
					] as [string, string][]
				).map(([t, l]) => (
					<div key={t} className="flex items-center gap-[5px]">
						<div
							style={{
								width: "9px",
								height: "9px",
								borderRadius: "3px",
								background: NODE_STYLE[t].bg,
								border: `1.5px solid ${NODE_STYLE[t].border}`,
							}}
						/>
						<span className="text-[10.5px] text-[#9a8a78] font-sans">{l}</span>
					</div>
				))}
				<span className="ml-auto text-[10.5px] text-[#b8a898] font-sans italic">
					Hover to explore connections · illustrative
				</span>
			</div>
		</div>
	);
}

// ─── Architecture Modal ───────────────────────────────────────────────────────

function ArchModal({
	resourceName,
	onClose,
}: {
	resourceName: string;
	onClose: () => void;
}) {
	return (
		<div
			className="fixed inset-0 z-50 flex items-center justify-center bg-[rgba(42,32,24,0.3)] backdrop-blur-[3px]"
			onClick={onClose}
		>
			<div
				className="bg-card rounded-2xl border border-[#e0d8cc] shadow-[0_20px_60px_rgba(42,32,24,0.2)] w-[min(860px,calc(100vw-48px))] overflow-hidden flex flex-col"
				onClick={(e) => {
					e.stopPropagation();
				}}
			>
				<div className="px-4 py-2.5 border-b border-[#ede7dd] flex items-center justify-between">
					<span className="font-serif text-[15px]">
						Architecture ·{" "}
						<span className="text-[#a0907e] font-sans font-normal text-[13px]">
							{resourceName}
						</span>
					</span>
					<button
						onClick={onClose}
						className="bg-transparent border-none cursor-pointer text-[#8a7a68] p-1 shrink-0"
					>
						<svg
							width="16"
							height="16"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							strokeWidth="2"
						>
							<line x1="18" y1="6" x2="6" y2="18" />
							<line x1="6" y1="6" x2="18" y2="18" />
						</svg>
					</button>
				</div>
				<ArchDiagram resourceName={resourceName} />
			</div>
		</div>
	);
}

// ─── Spec Diff Modal ──────────────────────────────────────────────────────────

interface DiffRow {
	key: string;
	label: string;
	current: string;
	old: string;
	changed: boolean;
}

function buildDiff(current: Deployment, old: Deployment): DiffRow[] {
	const rows: DiffRow[] = [];
	const cs = getServiceSpec(current);
	const os = getServiceSpec(old);
	if (!cs || !os) return rows;

	const row = (key: string, label: string, cv: unknown, ov: unknown) => {
		const c = String(cv ?? "—"),
			o = String(ov ?? "—");
		rows.push({ key, label, current: c, old: o, changed: c !== o });
	};

	row("image", "Image", cs.build?.image, os.build?.image);
	row("cpu", "CPU limit", cs.cpu, os.cpu);
	row("memory", "Memory limit", cs.memory, os.memory);
	row("minReplicas", "Min replicas", cs.minReplicas, os.minReplicas);
	row("maxReplicas", "Max replicas", cs.maxReplicas, os.maxReplicas);
	row("port", "Port", cs.port, os.port);

	const SENSITIVE = [
		"DATABASE_URL",
		"STRIPE_KEY",
		"REDIS_URL",
		"SECRET",
		"PASSWORD",
		"TOKEN",
	];
	const csEnv = cs.env,
		osEnv = os.env;
	const allKeys = new Set([...Object.keys(csEnv), ...Object.keys(osEnv)]);
	allKeys.forEach((k) => {
		const cv = csEnv[k],
			ov = osEnv[k];
		if (cv !== ov) {
			const mask = SENSITIVE.some((s) => k.toUpperCase().includes(s));
			rows.push({
				key: `env_${k}`,
				label: `env.${k}`,
				current: cv !== undefined ? (mask ? "••••••" : cv) : "—",
				old: ov !== undefined ? (mask ? "••••••" : ov) : "—",
				changed: true,
			});
		}
	});
	return rows;
}

function SpecDiffModal({
	current,
	old,
	onClose,
}: {
	current: Deployment;
	old: Deployment;
	onClose: () => void;
}) {
	const rows = buildDiff(current, old);
	const changed = rows.filter((r) => r.changed);
	const same = rows.filter((r) => !r.changed);

	return (
		<div
			className="fixed inset-0 z-50 flex items-center justify-center bg-[rgba(42,32,24,0.3)] backdrop-blur-[3px]"
			onClick={onClose}
		>
			<div
				className="bg-card rounded-2xl border border-[#e0d8cc] shadow-[0_20px_60px_rgba(42,32,24,0.2)] w-[min(640px,calc(100vw-48px))] max-h-[80vh] overflow-hidden flex flex-col"
				onClick={(e) => {
					e.stopPropagation();
				}}
			>
				<div className="px-6 pt-5 pb-4 border-b border-[#ede7dd] flex items-start justify-between gap-4">
					<div>
						<div className="font-serif text-[18px] mb-[6px]">Spec diff</div>
						<div className="text-[12px] text-[#a0907e] flex gap-2 items-center flex-wrap">
							<span className="font-mono bg-[#ede7dd] px-[7px] py-[2px] rounded">
								{relativeTime(old.createdAt)}
							</span>
							<span className="text-[#c4956a]">→</span>
							<span className="font-mono bg-[#eaf2ed] text-[#3a6b4a] px-[7px] py-[2px] rounded">
								{relativeTime(current.createdAt)} (current)
							</span>
						</div>
					</div>
					<button
						onClick={onClose}
						className="bg-transparent border-none cursor-pointer text-[#8a7a68] p-1 shrink-0"
					>
						<svg
							width="18"
							height="18"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							strokeWidth="2"
						>
							<line x1="18" y1="6" x2="6" y2="18" />
							<line x1="6" y1="6" x2="18" y2="18" />
						</svg>
					</button>
				</div>
				<div className="overflow-y-auto px-6 pb-6 pt-4">
					{changed.length === 0 ? (
						<div className="text-center py-8 text-[#a0907e] text-sm">
							No spec changes between these deployments.
						</div>
					) : (
						<>
							<div className="text-[11px] font-semibold text-[#8b2e2e] tracking-[0.07em] uppercase mb-2">
								{changed.length} change{changed.length !== 1 ? "s" : ""}
							</div>
							<div className="flex flex-col gap-1 mb-5">
								{changed.map((r) => (
									<div
										key={r.key}
										className="grid grid-cols-[140px_1fr_1fr] gap-2 p-[9px_12px] bg-[#fff8f0] rounded-lg border border-[#f0ddc8] items-start"
									>
										<span className="font-mono text-[11px] text-[#8a7a68] pt-3.5">
											{r.label}
										</span>
										<div className="flex flex-col gap-0.5">
											<span className="text-[9px] text-[#c0392b] font-semibold tracking-[0.06em] uppercase">
												Before
											</span>
											<span className="font-mono text-[11px] text-[#8b2e2e] bg-[#fdeaea] px-[7px] py-[3px] rounded-[5px] break-all">
												{r.old}
											</span>
										</div>
										<div className="flex flex-col gap-0.5">
											<span className="text-[9px] text-[#3a6b4a] font-semibold tracking-[0.06em] uppercase">
												After
											</span>
											<span className="font-mono text-[11px] text-[#3a6b4a] bg-[#eaf2ed] px-[7px] py-[3px] rounded-[5px] break-all">
												{r.current}
											</span>
										</div>
									</div>
								))}
							</div>
						</>
					)}
					{same.length > 0 && (
						<>
							<div className="text-[11px] font-semibold text-[#a0907e] tracking-[0.07em] uppercase mb-2">
								Unchanged
							</div>
							<div className="flex flex-col gap-[3px]">
								{same.map((r) => (
									<div
										key={r.key}
										className="grid grid-cols-[140px_1fr] gap-2 p-[7px_12px] rounded-[7px] items-center"
									>
										<span className="font-mono text-[11px] text-[#b0a090]">
											{r.label}
										</span>
										<span className="font-mono text-[11px] text-[#8a7a68]">
											{r.current}
										</span>
									</div>
								))}
							</div>
						</>
					)}
				</div>
			</div>
		</div>
	);
}

// ─── Activity Sheet ───────────────────────────────────────────────────────────

function ActivitySheet({
	open,
	onClose,
	resourceId,
}: {
	open: boolean;
	onClose: () => void;
	resourceId: string;
}) {
	const { events } = useStreamEvents(resourceId);
	const [filter, setFilter] = useState<"all" | "warning" | "normal">("all");

	const filtered =
		filter === "all"
			? events
			: filter === "warning"
				? events.filter((e) => e.severity === "Warning")
				: events.filter((e) => e.severity === "Normal");

	const filters = [
		{ id: "all" as const, label: "All" },
		{ id: "warning" as const, label: "Warnings" },
		{ id: "normal" as const, label: "Normal" },
	];

	return (
		<>
			<div
				className={cn(
					"fixed inset-0 bg-[rgba(42,32,24,0.12)] z-40 transition-opacity duration-280 ease-in",
					open
						? "opacity-100 pointer-events-auto"
						: "opacity-0 pointer-events-none",
				)}
				onClick={onClose}
			/>
			<div
				className={cn(
					"fixed top-0 right-0 bottom-0 w-[min(420px,100vw)] bg-card border-l border-[#e8e0d4] z-50 flex flex-col shadow-[-8px_0_40px_rgba(42,32,24,0.12)] transition-transform duration-[320ms] ease-[cubic-bezier(0.32,0.72,0,1)]",
					open ? "translate-x-0" : "translate-x-full",
				)}
			>
				<div className="px-[22px] pt-5 border-b border-[#e8e0d4]">
					<div className="flex items-center justify-between mb-3.5">
						<span className="font-serif text-[18px]">Activity</span>
						<button
							onClick={onClose}
							className="bg-transparent border-none cursor-pointer text-[#8a7a68] p-1 rounded-sm"
						>
							<svg
								width="17"
								height="17"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<line x1="18" y1="6" x2="6" y2="18" />
								<line x1="6" y1="6" x2="18" y2="18" />
							</svg>
						</button>
					</div>
					<div className="flex gap-0.5 -mb-px">
						{filters.map((f) => (
							<button
								key={f.id}
								onClick={() => {
									setFilter(f.id);
								}}
								className={cn(
									"bg-transparent border-none cursor-pointer px-3 py-[7px] text-xs font-medium font-sans transition-all duration-[140ms] border-b-2",
									filter === f.id
										? "text-foreground border-[#c4956a]"
										: "text-[#8a7a68] border-transparent",
								)}
							>
								{f.label}
							</button>
						))}
					</div>
				</div>
				<div className="flex-1 overflow-y-auto px-[22px] py-4">
					{filtered.length === 0 ? (
						<div className="text-center py-10 text-[#a0907e] text-[13px]">
							No events yet
						</div>
					) : (
						<div className="flex flex-col gap-0.5">
							{filtered.map((ev, i) => {
								const warn = ev.severity === "Warning";
								const cfg = warn
									? {
											icon: "!",
											bg: "#fdf3e3",
											color: "#9c6b1e",
											dot: "#d4870a",
										}
									: {
											icon: "✓",
											bg: "#eaf2ed",
											color: "#3a6b4a",
											dot: "#4a7c59",
										};
								return (
									<div
										key={i}
										className="flex gap-3 p-[10px_12px] rounded-md transition-colors duration-120 hover:bg-[#f2ece2]"
									>
										<div
											className="w-7 h-7 rounded-lg shrink-0 flex items-center justify-center text-xs font-bold mt-px"
											style={{
												background: cfg.bg,
												border: `1px solid ${cfg.dot}30`,
												color: cfg.color,
											}}
										>
											{cfg.icon}
										</div>
										<div className="flex-1 min-w-0">
											<div className="text-[13px] font-medium text-foreground mb-0.5 leading-[1.3]">
												{ev.eventType}
											</div>
											<div className="text-[11px] text-[#8a7a68] mb-[5px] leading-[1.4]">
												{ev.message}
											</div>
											<div className="flex items-center gap-2">
												<span className="text-[10px] text-[#b0a090] font-mono">
													{new Date(ev.timestamp).toLocaleTimeString()}
												</span>
												{ev.pod && (
													<span className="text-[10px] bg-[#ede7dd] text-[#8a7a68] px-[6px] py-px rounded font-mono">
														{ev.pod}
													</span>
												)}
											</div>
										</div>
									</div>
								);
							})}
						</div>
					)}
				</div>
			</div>
		</>
	);
}

// ─── Settings Sheet ───────────────────────────────────────────────────────────

const CPU_OPTIONS = [
	"100m",
	"250m",
	"500m",
	"750m",
	"1000m",
	"1250m",
	"1500m",
	"1750m",
	"2000m",
];
const MEM_OPTIONS = [
	"256Mi",
	"512Mi",
	"768Mi",
	"1Gi",
	"1.25Gi",
	"1.5Gi",
	"2Gi",
];

interface SettingsSheetProps {
	open: boolean;
	onClose: () => void;
	resourceId: string;
	resourceName: string;
	domains: ResourceDomain[];
	activeDep: Deployment | undefined;
}

function SettingsSheet({
	open,
	onClose,
	resourceId,
	resourceName: initialName,
	domains,
	activeDep,
}: SettingsSheetProps) {
	const navigate = useNavigate();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();

	// Scale state
	const svc = activeDep ? getServiceSpec(activeDep) : undefined;
	const initCpuIdx = svc?.cpu ? CPU_OPTIONS.indexOf(svc.cpu) : -1;
	const initMemIdx = svc?.memory ? MEM_OPTIONS.indexOf(svc.memory) : -1;
	const [cpuIndex, setCpuIndex] = useState(initCpuIdx >= 0 ? initCpuIdx : 4);
	const [memoryIndex, setMemoryIndex] = useState(
		initMemIdx >= 0 ? initMemIdx : 1,
	);
	const [replicas, setReplicas] = useState(activeDep?.replicas ?? 1);

	useEffect(() => {
		if (!open) return;
		const s = activeDep ? getServiceSpec(activeDep) : undefined;
		const ci = s?.cpu ? CPU_OPTIONS.indexOf(s.cpu) : -1;
		const mi = s?.memory ? MEM_OPTIONS.indexOf(s.memory) : -1;
		setCpuIndex(ci >= 0 ? ci : 4);
		setMemoryIndex(mi >= 0 ? mi : 1);
		setReplicas(activeDep?.replicas ?? 1);
	}, [open, activeDep]);

	// Name state
	const [name, setName] = useState(initialName);
	useEffect(() => {
		if (open) setName(initialName);
	}, [open, initialName]);

	// Domain state — editable subdomain
	const primaryDomain = domains[0];
	const domainStr = primaryDomain?.domain ?? "";
	const dotIdx = domainStr.indexOf(".");
	const initSubdomain = dotIdx > -1 ? domainStr.slice(0, dotIdx) : domainStr;
	const domainSuffix = dotIdx > -1 ? domainStr.slice(dotIdx) : "";
	const [subdomain, setSubdomain] = useState(initSubdomain);
	useEffect(() => {
		if (!open) return;
		const d = domains[0]?.domain ?? "";
		const di = d.indexOf(".");
		setSubdomain(di > -1 ? d.slice(0, di) : d);
	}, [open, domains]);

	// Danger zone
	const [confirmDelete, setConfirmDelete] = useState(false);

	const { mutate: scale, isPending: scaling } = useMutation(scaleResource);
	const { mutate: update, isPending: updating } = useMutation(updateResource);
	const { mutate: del, isPending: deleting } = useMutation(deleteResource);
	const { mutate: updateDomain, isPending: savingDomain } =
		useMutation(updateResourceDomain);

	const handleScale = () => {
		scale(
			{
				resourceId,
				replicas,
				cpu: CPU_OPTIONS[cpuIndex],
				memory: MEM_OPTIONS[memoryIndex],
			},
			{
				onSuccess: () => {
					toast.success("Scaling applied");
				},
				onError: () => {
					toast.error("Failed to scale");
				},
			},
		);
	};

	const handleNameSave = () => {
		if (!name.trim() || name.trim() === initialName) return;
		update(
			{ resourceId, name: name.trim() },
			{
				onSuccess: () => {
					toast.success("Resource renamed");
				},
				onError: () => {
					toast.error("Failed to rename");
				},
			},
		);
	};

	const handleDomainSave = () => {
		if (
			!primaryDomain?.id ||
			!subdomain.trim() ||
			subdomain.trim() === initSubdomain
		)
			return;
		const newDomain = `${subdomain.trim()}${domainSuffix}`;
		updateDomain(
			{ domainId: primaryDomain.id, domain: newDomain },
			{
				onSuccess: () => {
					toast.success("Domain updated");
				},
				onError: () => {
					toast.error("Failed to update domain");
				},
			},
		);
	};

	const handleDelete = () => {
		del(
			{ resourceId },
			{
				onSuccess: () => {
					toast.success("Resource deleted");
					onClose();
					if (activeOrgId && activeWorkspaceId)
						void navigate(`/org/${activeOrgId}/wks/${activeWorkspaceId}`);
				},
				onError: () => {
					toast.error("Failed to delete resource");
				},
			},
		);
	};

	const sec = (title: string) => (
		<div className="text-[10px] font-bold text-[#b0a090] tracking-[0.08em] uppercase mb-3">
			{title}
		</div>
	);
	const divider = <div className="border-t border-[#ede7dd] my-6" />;

	return (
		<>
			<div
				className={cn(
					"fixed inset-0 bg-[rgba(42,32,24,0.12)] z-40 transition-opacity duration-280 ease-in",
					open
						? "opacity-100 pointer-events-auto"
						: "opacity-0 pointer-events-none",
				)}
				onClick={onClose}
			/>
			<div
				className={cn(
					"fixed top-0 right-0 bottom-0 w-[min(440px,100vw)] bg-card border-l border-[#e8e0d4] z-50 flex flex-col shadow-[-8px_0_40px_rgba(42,32,24,0.12)] transition-transform duration-320 ease-[cubic-bezier(0.32,0.72,0,1)]",
					open ? "translate-x-0" : "translate-x-full",
				)}
			>
				{/* header */}
				<div className="px-[22px] py-4 border-b border-[#e8e0d4] flex items-center justify-between">
					<span className="font-serif text-[18px]">Settings</span>
					<button
						onClick={onClose}
						className="bg-transparent border-none cursor-pointer text-[#8a7a68] p-1 rounded-sm"
					>
						<svg
							width="17"
							height="17"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							strokeWidth="2"
						>
							<line x1="18" y1="6" x2="6" y2="18" />
							<line x1="6" y1="6" x2="18" y2="18" />
						</svg>
					</button>
				</div>

				{/* body */}
				<div className="flex-1 overflow-y-auto px-[22px] py-6">
					{/* Scale */}
					{sec("Scale")}
					<div className="flex flex-col gap-5">
						<div>
							<div className="flex items-center justify-between mb-2">
								<span className="text-[13px] text-[#4a3c30] font-medium">
									Replicas
								</span>
								<span className="text-[13px] font-semibold text-foreground">
									{replicas}
								</span>
							</div>
							<Input
								type="number"
								min="1"
								max="20"
								value={replicas}
								onChange={(e) => {
									const n = parseInt(e.target.value, 10);
									if (!isNaN(n) && n > 0) setReplicas(n);
								}}
								className="w-24 text-center font-mono text-[13px]"
							/>
						</div>
						<div>
							<div className="flex items-center justify-between mb-2.5">
								<span className="text-[13px] text-[#4a3c30] font-medium">
									CPU
								</span>
								<span className="font-mono text-[12px] font-semibold text-foreground bg-[#ede7dd] px-2 py-0.5 rounded-[5px]">
									{CPU_OPTIONS[cpuIndex]}
								</span>
							</div>
							<Slider
								value={[cpuIndex]}
								onValueChange={(v) => {
									setCpuIndex(v[0]);
								}}
								min={0}
								max={CPU_OPTIONS.length - 1}
								step={1}
								className="w-full"
							/>
							<div className="flex justify-between mt-1.5">
								<span className="text-[10px] text-[#b0a090]">100m</span>
								<span className="text-[10px] text-[#b0a090]">2000m</span>
							</div>
						</div>
						<div>
							<div className="flex items-center justify-between mb-2.5">
								<span className="text-[13px] text-[#4a3c30] font-medium">
									Memory
								</span>
								<span className="font-mono text-[12px] font-semibold text-foreground bg-[#ede7dd] px-2 py-0.5 rounded-[5px]">
									{MEM_OPTIONS[memoryIndex]}
								</span>
							</div>
							<Slider
								value={[memoryIndex]}
								onValueChange={(v) => {
									setMemoryIndex(v[0]);
								}}
								min={0}
								max={MEM_OPTIONS.length - 1}
								step={1}
								className="w-full"
							/>
							<div className="flex justify-between mt-1.5">
								<span className="text-[10px] text-[#b0a090]">256Mi</span>
								<span className="text-[10px] text-[#b0a090]">2Gi</span>
							</div>
						</div>
						<button
							onClick={handleScale}
							disabled={scaling}
							className={cn(
								"self-start inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-[#2a2018] text-[#f7f3ec] border-none text-[13px] font-medium font-sans",
								scaling
									? "opacity-60 cursor-not-allowed"
									: "cursor-pointer hover:bg-[#3d2f20] transition-colors",
							)}
						>
							{scaling && (
								<svg
									className="animate-spin"
									width="12"
									height="12"
									viewBox="0 0 24 24"
									fill="none"
									stroke="currentColor"
									strokeWidth="2"
								>
									<path d="M21 12a9 9 0 1 1-6.219-8.56" />
								</svg>
							)}
							{scaling ? "Applying…" : "Apply"}
						</button>
					</div>

					{divider}

					{/* Domain */}
					{sec("Domain")}
					{primaryDomain ? (
						<div className="flex flex-col gap-2">
							<div className="flex gap-1.5 items-center">
								<Input
									value={subdomain}
									onChange={(e) => {
										setSubdomain(
											e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ""),
										);
									}}
									placeholder="subdomain"
									className="font-mono text-[12px] flex-1"
								/>
								{domainSuffix && (
									<span className="font-mono text-[12px] text-[#a0907e] bg-[#f0ebe3] px-2.5 py-2 rounded-[7px] border border-[#e0d8cc] whitespace-nowrap shrink-0">
										{domainSuffix}
									</span>
								)}
							</div>
							<div className="flex gap-2 items-center">
								<a
									href={`https://${domainStr}`}
									target="_blank"
									rel="noopener noreferrer"
									className="font-mono text-[11px] text-[#4a6b9c]! no-underline flex-1 overflow-hidden text-ellipsis whitespace-nowrap"
								>
									↗ {subdomain || "…"}
									{domainSuffix}
								</a>
								<button
									onClick={handleDomainSave}
									disabled={
										savingDomain ||
										!subdomain.trim() ||
										subdomain.trim() === initSubdomain
									}
									className={cn(
										"inline-flex items-center gap-[5px] px-3 py-1.5 rounded-[7px] bg-transparent text-[#4a3c30] border border-[#ddd5c8] cursor-pointer text-xs font-sans whitespace-nowrap",
										savingDomain ||
											!subdomain.trim() ||
											subdomain.trim() === initSubdomain
											? "opacity-50"
											: "",
									)}
								>
									{savingDomain ? "Saving…" : "Save"}
								</button>
							</div>
						</div>
					) : (
						<div className="text-[13px] text-[#a0907e]">
							No domain configured
						</div>
					)}

					{divider}

					{/* General */}
					{sec("General")}
					<div className="flex gap-2 items-center">
						<Input
							value={name}
							onChange={(e) => {
								setName(e.target.value);
							}}
							placeholder="Resource name"
							className="font-sans text-[13px] flex-1"
						/>
						<button
							onClick={handleNameSave}
							disabled={updating || !name.trim() || name.trim() === initialName}
							className={cn(
								"inline-flex items-center gap-[5px] px-3.5 py-2 rounded-lg bg-transparent text-[#4a3c30] border border-[#ddd5c8] cursor-pointer text-[13px] font-medium font-sans whitespace-nowrap",
								updating || !name.trim() || name.trim() === initialName
									? "opacity-50"
									: "",
							)}
						>
							{updating ? "Saving…" : "Save"}
						</button>
					</div>

					{divider}

					{/* Danger zone */}
					{sec("Danger Zone")}
					{!confirmDelete ? (
						<button
							onClick={() => {
								setConfirmDelete(true);
							}}
							className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg bg-[#fdeaea] text-[#8b2e2e] border border-[#f0c8c8] cursor-pointer text-[13px] font-medium font-sans"
						>
							<svg
								width="13"
								height="13"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<polyline points="3 6 5 6 21 6" />
								<path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
								<path d="M10 11v6M14 11v6M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" />
							</svg>
							Delete resource
						</button>
					) : (
						<div className="p-3.5 bg-[#fdeaea] border border-[#f0c8c8] rounded-md">
							<p className="text-[13px] text-[#8b2e2e] mb-3 font-medium">
								Are you sure? This cannot be undone.
							</p>
							<div className="flex gap-2">
								<button
									onClick={handleDelete}
									disabled={deleting}
									className={cn(
										"inline-flex items-center gap-[5px] px-3.5 py-[7px] rounded-[7px] bg-[#8b2e2e] text-white border-none text-xs font-semibold font-sans",
										deleting
											? "opacity-70 cursor-not-allowed"
											: "cursor-pointer",
									)}
								>
									{deleting ? "Deleting…" : "Yes, delete"}
								</button>
								<button
									onClick={() => {
										setConfirmDelete(false);
									}}
									className="px-3 py-[7px] rounded-[7px] bg-transparent text-[#6b5d4f] border border-[#ddd5c8] cursor-pointer text-xs font-sans"
								>
									Cancel
								</button>
							</div>
						</div>
					)}
				</div>
			</div>
		</>
	);
}

// ─── Main Page ────────────────────────────────────────────────────────────────

export function ResourceDetails() {
	const { resourceId } = useParams<{ resourceId: string }>();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const navigate = useNavigate();

	const {
		resource: resourceResponse,
		deployments,
		isLoading,
		error,
	} = useResourceDetails(resourceId ?? "");
	const resource = resourceResponse?.resource;

	const [diff, setDiff] = useState<{
		current: Deployment;
		old: Deployment;
	} | null>(null);
	const [activityOpen, setActivityOpen] = useState(false);
	const [settingsOpen, setSettingsOpen] = useState(false);
	const [archOpen, setArchOpen] = useState(false);
	const [deployDialogOpen, setDeployDialogOpen] = useState(false);
	const [rollingBackId, setRollingBackId] = useState<string | null>(null);

	const anySheetOpen = activityOpen || settingsOpen;

	const redeployMutation = useMutation(createDeployment);
	const deployMutation = useMutation(createDeployment);

	useEffect(() => {
		if (!resourceId) return;
		const unsub = subscribeToEvents(`resource:${resourceId}`, () => {
			/* no-op */
		});
		return unsub;
	}, [resourceId]);

	// ── loading / error states ───────────────────────────────────────────────
	if (!resourceId) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-medium mb-2">
							Invalid Resource ID
						</p>
						<p className="text-sm text-muted-foreground">
							The resource ID is missing from the URL
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="inline-flex flex-col gap-2 items-center">
					<Loader className="w-8 h-8" />
					<p className="text-sm text-muted-foreground">Loading resource…</p>
				</div>
			</div>
		);
	}

	if (error) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-medium mb-4">
							Error Loading Resource
						</p>
						<p className="text-sm text-muted-foreground">
							{getErrorMessage(error, "Failed to load resource")}
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	if (!resource) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-medium mb-2">
							Resource Not Found
						</p>
						<p className="text-sm text-muted-foreground">
							The resource with ID {resourceId} does not exist
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	// ── derived data ─────────────────────────────────────────────────────────
	const statusKey = statusKeyFromLabel(getStatusLabel(resource.status));
	const st = STATUS_CFG[statusKey] ?? STATUS_CFG.pending;
	const activeDep = deployments.find((d) => d.isActive) ?? deployments[0];
	const primaryDomain = resource.domains?.[0]?.domain;
	const activeSvc = activeDep ? getServiceSpec(activeDep) : undefined;

	const handleRedeploy = async () => {
		if (!activeDep?.spec) {
			toast.error("No active deployment to redeploy");
			return;
		}
		try {
			await redeployMutation.mutateAsync({
				resourceId,
				region: activeDep.region,
				spec: activeDep.spec,
				environmentId: activeDep.environmentId,
			});
			toast.success("Redeployment started");
		} catch {
			toast.error("Failed to trigger redeployment");
		}
	};

	const handleRollback = async (dep: Deployment) => {
		if (!dep.spec) return;
		setRollingBackId(dep.id);
		try {
			await redeployMutation.mutateAsync({
				resourceId,
				region: dep.region,
				spec: dep.spec,
				environmentId: dep.environmentId,
			});
			toast.success("Rollback started");
		} catch {
			toast.error("Rollback failed");
		} finally {
			setRollingBackId(null);
		}
	};

	const handleDeploy = async (values: DeploymentWizardValues) => {
		try {
			await deployMutation.mutateAsync({
				resourceId,
				region: values.region,
				spec: {
					spec: {
						case: "service",
						value: {
							build: { type: "image", image: values.imageUrl },
							cpu: values.cpu,
							memory: values.memory,
							minReplicas: values.replicas,
							maxReplicas: values.replicas,
							port: values.port,
							env: values.envVars,
						},
					},
				},
				environmentId: deployments[0]?.environmentId,
			});
			setDeployDialogOpen(false);
			toast.success("Deployment started");
		} catch {
			toast.error("Failed to start deployment");
		}
	};

	const redeploying = redeployMutation.isPending;

	// ── per-region card data ─────────────────────────────────────────────────
	const regionCards = resource.regions.map((r, idx) => {
		const rDep =
			deployments.find((d) => d.region === r.region && d.isActive) ?? activeDep;
		const rSvc = rDep ? getServiceSpec(rDep) : activeSvc;
		const statusKey2 = (() => {
			switch (r.status) {
				case 1:
					return "deploying";
				case 2:
					return "healthy";
				case 3:
					return "degraded";
				case 4:
					return "failed";
				default:
					return "pending";
			}
		})();
		const rs = STATUS_CFG[statusKey2] ?? STATUS_CFG.pending;

		const cpuLimit = parseCpuMilli(rSvc?.cpu ?? "500m");
		const memLimit = parseMemMi(rSvc?.memory ?? "512Mi");
		const cpuPct = mockUsagePct(statusKey2, idx * 3);
		const memPct = mockUsagePct(statusKey2, idx * 3 + 1);
		const cpuUsed = Math.round((cpuLimit * cpuPct) / 100);
		const memUsed = Math.round((memLimit * memPct) / 100);
		const replicas = rDep?.replicas ?? rSvc?.minReplicas ?? 1;

		return {
			region: r.region,
			isPrimary: r.isPrimary,
			rs,
			statusKey2,
			cpuUsed,
			cpuLimit,
			memUsed,
			memLimit,
			memPct,
			cpuPct,
			replicas,
		};
	});

	// ── render ───────────────────────────────────────────────────────────────
	return (
		<div
			className={cn(
				"font-sans text-foreground transition-[padding-right] duration-320 ease-[cubic-bezier(0.32,0.72,0,1)]",
				anySheetOpen ? "pr-[440px]" : "pr-0",
			)}
		>
			<div
				className={cn(
					"max-w-7xl mx-auto transition-[padding] duration-320 ease-[cubic-bezier(0.32,0.72,0,1)]",
					anySheetOpen ? "pt-7 px-4 pb-20" : "pt-7 px-10 pb-20",
				)}
			>
				{/* ── Header ── */}
				<div className="flex items-start justify-between flex-wrap gap-4 mb-7">
					<div>
						<div className="flex items-center gap-2.5 mb-2">
							<h1 className="font-serif text-[28px] font-normal tracking-[-0.3px] leading-[1.2] m-0">
								{resource.name}
							</h1>
							<span
								className="text-xs px-2.5 py-[3px] rounded-full font-semibold flex items-center gap-[5px] shrink-0"
								style={{ background: st.bg, color: st.color }}
							>
								<span
									className={cn(
										"w-[6px] h-[6px] rounded-full inline-block",
										statusKey !== "healthy" && "animate-status-pulse",
									)}
									style={{ background: st.dot }}
								/>
								{st.label}
							</span>
						</div>
						<div className="flex items-center gap-[7px] flex-wrap">
							{activeDep && (
								<span className="flex items-center gap-1 font-mono text-[11px] text-[#8a7a68] bg-[#ede7dd] px-2 py-[3px] rounded-[5px]">
									<svg
										width="9"
										height="9"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2.5"
									>
										<rect x="3" y="3" width="18" height="18" rx="2" />
										<path d="M9 9h6M9 12h6M9 15h4" />
									</svg>
									{shortId(activeDep.id)}
								</span>
							)}
							{activeDep && deploymentImage(activeDep) !== "—" && (
								<span className="font-mono text-[11px] text-[#6258a0] bg-[#eeecf8] px-2 py-[3px] rounded-[5px] flex items-center gap-1">
									<svg
										width="10"
										height="10"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
									>
										<rect x="2" y="7" width="20" height="14" rx="2" />
										<path d="M16 7V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v2" />
									</svg>
									{deploymentImage(activeDep).split("/").pop()}
								</span>
							)}
							{primaryDomain && (
								<a
									href={`https://${primaryDomain}`}
									target="_blank"
									rel="noopener noreferrer"
									className="font-mono text-[11px] text-[#4a6b9c]! bg-[#edf2f8] px-2 py-[3px] rounded-[5px] no-underline flex items-center gap-1"
								>
									<svg
										width="10"
										height="10"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
									>
										<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
										<polyline points="15 3 21 3 21 9" />
										<line x1="10" y1="14" x2="21" y2="3" />
									</svg>
									{primaryDomain}
								</a>
							)}
						</div>
					</div>

					<div className="flex gap-1.5 pt-1 items-center">
						<button
							className="inline-flex items-center gap-1.5 px-3.5 py-[7px] rounded-lg text-[13px] font-medium text-[#6b5d4f] border border-[#ddd5c8] bg-transparent hover:bg-[#f0ebe3] hover:border-[#c9bbad] transition-all cursor-pointer"
							onClick={() => {
								if (activeOrgId && activeWorkspaceId)
									void navigate(
										`/org/${activeOrgId}/wks/${activeWorkspaceId}/observability`,
									);
							}}
						>
							<svg
								width="13"
								height="13"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
							</svg>
							Telemetry
						</button>
						<button
							className="inline-flex items-center justify-center w-[34px] h-[34px] rounded-lg cursor-pointer transition-all bg-transparent border border-[#ddd5c8] hover:bg-[#f0ebe3] hover:border-[#c9bbad] text-[#6b5d4f]"
							title="Architecture"
							onClick={() => {
								setArchOpen(true);
							}}
						>
							<svg
								width="14"
								height="14"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<rect x="3" y="3" width="7" height="7" rx="1" />
								<rect x="14" y="3" width="7" height="7" rx="1" />
								<rect x="3" y="14" width="7" height="7" rx="1" />
								<path d="M17.5 14v3m0 3v.01M17.5 17h3m-6 0h.01" />
							</svg>
						</button>
						<button
							className={cn(
								"inline-flex items-center justify-center w-[34px] h-[34px] rounded-lg cursor-pointer transition-all bg-transparent border border-[#ddd5c8] hover:bg-[#f0ebe3] hover:border-[#c9bbad]",
								activityOpen
									? "bg-[#f0ebe3] border-[#c9bbad] text-foreground"
									: "text-[#6b5d4f]",
							)}
							title="Activity"
							onClick={() => {
								setActivityOpen((v) => !v);
								setSettingsOpen(false);
							}}
						>
							<svg
								width="14"
								height="14"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
								<path d="M13.73 21a2 2 0 0 1-3.46 0" />
							</svg>
						</button>
						<button
							className={cn(
								"inline-flex items-center justify-center w-[34px] h-[34px] rounded-lg cursor-pointer transition-all bg-transparent border border-[#ddd5c8] hover:bg-[#f0ebe3] hover:border-[#c9bbad]",
								settingsOpen
									? "bg-[#f0ebe3] border-[#c9bbad] text-foreground"
									: "text-[#6b5d4f]",
							)}
							title="Settings"
							onClick={() => {
								setSettingsOpen((v) => !v);
								setActivityOpen(false);
							}}
						>
							<svg
								width="14"
								height="14"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
							</svg>
						</button>
						{activeDep ? (
							<button
								className={cn(
									"inline-flex items-center gap-1.5 px-3.5 py-[7px] rounded-lg bg-[#2a2018] text-[#f7f3ec] text-[13px] font-medium border-none transition-all",
									redeploying
										? "opacity-55 cursor-not-allowed"
										: "cursor-pointer hover:bg-[#3d2f20]",
								)}
								onClick={() => {
									void handleRedeploy();
								}}
								disabled={redeploying}
							>
								{redeploying ? (
									<svg
										className="animate-spin"
										width="13"
										height="13"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
									>
										<path d="M21 12a9 9 0 1 1-6.219-8.56" />
									</svg>
								) : (
									<svg
										width="13"
										height="13"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
									>
										<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
										<path d="M3 3v5h5" />
									</svg>
								)}
								{redeploying ? "Deploying…" : "Redeploy"}
							</button>
						) : (
							<button
								className="inline-flex items-center gap-1.5 px-3.5 py-[7px] rounded-lg bg-[#2a2018] text-[#f7f3ec] text-[13px] font-medium border-none cursor-pointer hover:bg-[#3d2f20] transition-all"
								onClick={() => {
									setDeployDialogOpen(true);
								}}
							>
								<svg
									width="13"
									height="13"
									viewBox="0 0 24 24"
									fill="none"
									stroke="currentColor"
									strokeWidth="2"
								>
									<path d="M12 5v14M5 12l7-7 7 7" />
								</svg>
								Deploy
							</button>
						)}
					</div>
				</div>

				{/* ── Resource cards ── */}
				{regionCards.length === 1 ? (
					/* Single region: expanded 3-card layout */
					(() => {
						const r = regionCards[0];
						return (
							<div className="grid grid-cols-3 gap-3 mb-5">
								{/* Replicas */}
								<div className="bg-card border border-[#e8e0d4] rounded-xl p-5">
									<div className="text-[11px] font-semibold text-[#a0907e] tracking-[0.08em] uppercase mb-2.5">
										Replicas
									</div>
									<div className="flex items-baseline gap-1">
										<span className="text-[28px] font-semibold font-serif text-foreground">
											{r.replicas}
										</span>
									</div>
									<div className="flex gap-[5px] mt-2.5 flex-wrap">
										{Array.from({ length: Math.min(r.replicas, 12) }).map(
											(_, i) => (
												<div
													key={i}
													className="w-2 h-2 rounded-full"
													style={{ background: r.rs.dot }}
												/>
											),
										)}
									</div>
								</div>

								{/* CPU */}
								<div className="bg-card border border-[#e8e0d4] rounded-xl p-5">
									<div className="text-[11px] font-semibold text-[#a0907e] tracking-[0.08em] uppercase mb-2.5">
										CPU
									</div>
									<div className="flex items-baseline gap-1">
										<span
											className={cn(
												"text-[28px] font-semibold font-serif",
												r.cpuPct > 80 ? "text-[#8b2e2e]" : "text-foreground",
											)}
										>
											{r.cpuUsed}
										</span>
										<span className="text-sm text-[#a0907e]">
											m / {r.cpuLimit}m
										</span>
									</div>
									<div className="mt-3">
										<div className="h-[3px] bg-[#e8e0d4] rounded-xs overflow-hidden">
											<div
												className="h-full rounded-xs transition-[width] duration-600"
												style={{
													width: `${r.cpuPct}%`,
													background:
														r.cpuPct > 80
															? "#c0392b"
															: r.cpuPct > 60
																? "#d4870a"
																: "#c4956a",
												}}
											/>
										</div>
										<div className="text-[11px] text-[#a0907e] mt-1">
											{r.cpuPct}% of limit
										</div>
									</div>
								</div>

								{/* Memory */}
								<div className="bg-card border border-[#e8e0d4] rounded-xl p-5">
									<div className="text-[11px] font-semibold text-[#a0907e] tracking-[0.08em] uppercase mb-2.5">
										Memory
									</div>
									<div className="flex items-baseline gap-1">
										<span
											className={cn(
												"text-[28px] font-semibold font-serif",
												r.memPct > 80 ? "text-[#8b2e2e]" : "text-foreground",
											)}
										>
											{r.memUsed}
										</span>
										<span className="text-sm text-[#a0907e]">
											MB / {r.memLimit}MB
										</span>
									</div>
									<div className="mt-3">
										<div className="h-[3px] bg-[#e8e0d4] rounded-xs overflow-hidden">
											<div
												className="h-full rounded-xs transition-[width] duration-600"
												style={{
													width: `${r.memPct}%`,
													background:
														r.memPct > 80
															? "#c0392b"
															: r.memPct > 60
																? "#d4870a"
																: "#4a7c59",
												}}
											/>
										</div>
										<div className="text-[11px] text-[#a0907e] mt-1">
											{r.memPct}% of limit
										</div>
									</div>
								</div>
							</div>
						);
					})()
				) : regionCards.length > 1 ? (
					/* Multi-region: one compact card per region */
					<div
						className="grid gap-2.5 mb-5"
						style={{
							gridTemplateColumns: `repeat(${Math.min(regionCards.length, 4)}, 1fr)`,
						}}
					>
						{regionCards.map((r) => (
							<div
								key={r.region}
								className={cn(
									"bg-card border rounded-xl px-[18px] py-4",
									r.statusKey2 === "degraded"
										? "border-[#e8d4a4]"
										: r.statusKey2 === "failed"
											? "border-[#e8c4c4]"
											: "border-[#e8e0d4]",
								)}
							>
								<div className="flex items-center justify-between mb-3">
									<div className="text-xs font-semibold text-[#4a3c30] flex items-center gap-[5px]">
										{r.region}
										{r.isPrimary && (
											<span className="text-[9px] text-[#b0a090] font-normal">
												primary
											</span>
										)}
									</div>
									<div className="flex items-center gap-1">
										<span
											className="w-[5px] h-[5px] rounded-full"
											style={{ background: r.rs.dot }}
										/>
										<span
											className="text-[11px] font-medium"
											style={{ color: r.rs.color }}
										>
											{r.rs.label}
										</span>
									</div>
								</div>
								<div className="flex flex-col gap-2.5">
									<div>
										<div className="flex justify-between mb-1">
											<span className="text-[10px] text-[#a0907e] uppercase tracking-[0.07em] font-semibold">
												CPU
											</span>
											<span
												className={cn(
													"font-mono text-[10px]",
													r.cpuPct > 70 ? "text-[#9c6b1e]" : "text-[#6b5d4f]",
												)}
											>
												{r.cpuUsed}m / {r.cpuLimit}m
											</span>
										</div>
										<div className="h-[3px] bg-[#e8e0d4] rounded-[2px] overflow-hidden">
											<div
												className="h-full rounded-[2px] transition-[width] duration-[600ms]"
												style={{
													width: `${r.cpuPct}%`,
													background:
														r.cpuPct > 80
															? "#c0392b"
															: r.cpuPct > 60
																? "#d4870a"
																: "#c4956a",
												}}
											/>
										</div>
									</div>
									<div>
										<div className="flex justify-between mb-1">
											<span className="text-[10px] text-[#a0907e] uppercase tracking-[0.07em] font-semibold">
												Memory
											</span>
											<span
												className={cn(
													"font-mono text-[10px]",
													r.memPct > 70 ? "text-[#9c6b1e]" : "text-[#6b5d4f]",
												)}
											>
												{r.memUsed}MB / {r.memLimit}MB
											</span>
										</div>
										<div className="h-[3px] bg-[#e8e0d4] rounded-[2px] overflow-hidden">
											<div
												className="h-full rounded-[2px] transition-[width] duration-[600ms]"
												style={{
													width: `${r.memPct}%`,
													background:
														r.memPct > 80
															? "#c0392b"
															: r.memPct > 60
																? "#d4870a"
																: "#4a7c59",
												}}
											/>
										</div>
									</div>
									<div className="flex justify-between pt-0.5">
										<span className="text-[10px] text-[#a0907e] uppercase tracking-[0.07em] font-semibold">
											Replicas
										</span>
										<div className="flex gap-[3px] items-center">
											{Array.from({ length: Math.min(r.replicas, 8) }).map(
												(_, i) => (
													<div
														key={i}
														className="w-[5px] h-[5px] rounded-full"
														style={{ background: r.rs.dot }}
													/>
												),
											)}
											<span className="font-mono text-[10px] text-[#8a7a68] ml-1">
												{r.replicas}
											</span>
										</div>
									</div>
								</div>
							</div>
						))}
					</div>
				) : null}

				{/* ── Deployments ── */}
				<div className="bg-card border border-[#e8e0d4] rounded-xl overflow-hidden">
					<div className="px-5 py-3 border-b border-[#ede7dd] flex items-center justify-between">
						<span className="font-serif text-[17px]">Deployments</span>
						<span className="text-[11px] text-[#a0907e]">
							Click any past deployment to compare spec
						</span>
					</div>

					<div
						className="grid gap-2.5 px-[26px] py-2 border-b border-[#f0e8dc]"
						style={{
							gridTemplateColumns:
								"minmax(80px,1fr) minmax(120px,2fr) 130px 110px 90px 110px",
						}}
					>
						{["ID", "Image", "Status", "Region", "Time", ""].map((h, i) => (
							<span
								key={i}
								className="text-[10px] font-semibold text-[#b0a090] uppercase tracking-[0.07em]"
							>
								{h}
							</span>
						))}
					</div>

					<div className="p-1.5 px-2">
						{deployments.length === 0 ? (
							<div className="p-7 text-center text-[#a0907e] text-[13px]">
								No deployments yet
							</div>
						) : (
							deployments.map((dep) => {
								const ph =
									PHASE_CFG[dep.status] ??
									PHASE_CFG[DeploymentPhase.UNSPECIFIED];
								const isCurr = dep.isActive;
								const canDiff = !isCurr && activeDep && activeDep.id !== dep.id;
								return (
									<div
										key={dep.id}
										className={cn(
											"group grid gap-2.5 items-center px-3.5 py-3 rounded-[10px] transition-colors border-l-[3px]",
											canDiff
												? "cursor-pointer hover:bg-[#f2ece2]"
												: "cursor-default",
											isCurr
												? "bg-[#f7f3ee] border-[#c4956a]"
												: "border-transparent",
										)}
										style={{
											gridTemplateColumns:
												"minmax(80px,1fr) minmax(120px,2fr) 130px 110px 90px 110px",
										}}
										onClick={() => {
											if (canDiff && activeDep)
												setDiff({ current: activeDep, old: dep });
										}}
									>
										<span className="font-mono text-[11px] text-[#6b5d4f] overflow-hidden text-ellipsis whitespace-nowrap">
											{shortId(dep.id)}
										</span>
										<span className="font-mono text-[11px] text-[#6b5d4f] overflow-hidden text-ellipsis whitespace-nowrap">
											{deploymentImage(dep).split("/").pop() || "—"}
										</span>
										<span
											className="text-[11px] px-[9px] py-[3px] rounded-[10px] font-semibold inline-flex items-center gap-[5px] w-fit"
											style={{ background: ph.bg, color: ph.color }}
										>
											{dep.status === DeploymentPhase.DEPLOYING && (
												<span className="animate-spin inline-block w-[7px] h-[7px] border-[1.5px] border-current border-t-transparent rounded-full" />
											)}
											{ph.label}
										</span>
										<span className="text-[11px] text-[#8a7a68] font-mono overflow-hidden text-ellipsis whitespace-nowrap">
											{dep.region || "—"}
										</span>
										<span className="text-[11px] text-[#a0907e]">
											{relativeTime(dep.createdAt)}
										</span>
										<div className="flex gap-1.5 items-center">
											{isCurr && (
												<span className="text-[10px] text-[#c4956a] font-semibold">
													current
												</span>
											)}
											{!isCurr && dep.status === DeploymentPhase.SUCCEEDED && (
												<button
													className={cn(
														"inline-flex items-center gap-1.5 px-[9px] py-[3px] rounded-lg text-[11px] font-medium text-[#6b5d4f] border border-[#ddd5c8] bg-transparent cursor-pointer transition-all hover:bg-[#f0ebe3] hover:border-[#c9bbad]",
														rollingBackId === dep.id &&
															"opacity-60 cursor-not-allowed",
													)}
													disabled={rollingBackId === dep.id}
													onClick={(e) => {
														e.stopPropagation();
														void handleRollback(dep);
													}}
												>
													{rollingBackId === dep.id
														? "Rolling back…"
														: "Rollback"}
												</button>
											)}
											{canDiff && (
												<span className="opacity-0 group-hover:opacity-100 transition-opacity text-[11px] text-[#c4956a] font-medium">
													diff →
												</span>
											)}
										</div>
									</div>
								);
							})
						)}
					</div>
				</div>
			</div>

			<ActivitySheet
				open={activityOpen}
				onClose={() => {
					setActivityOpen(false);
				}}
				resourceId={resourceId}
			/>

			<SettingsSheet
				open={settingsOpen}
				onClose={() => {
					setSettingsOpen(false);
				}}
				resourceId={resourceId}
				resourceName={resource.name}
				domains={resource.domains ?? []}
				activeDep={activeDep}
			/>

			{archOpen && (
				<ArchModal
					resourceName={resource.name}
					onClose={() => {
						setArchOpen(false);
					}}
				/>
			)}

			{diff && (
				<SpecDiffModal
					current={diff.current}
					old={diff.old}
					onClose={() => {
						setDiff(null);
					}}
				/>
			)}

			<DeploymentWizard
				open={deployDialogOpen}
				onClose={() => {
					setDeployDialogOpen(false);
				}}
				title="Deploy"
				submitLabel="Deploy"
				onSubmit={handleDeploy}
				isSubmitting={deployMutation.isPending}
			/>
		</div>
	);
}
