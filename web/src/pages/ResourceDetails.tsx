import { useResourceDetails } from "@/hooks/useResourceDetails";
import { useStreamEvents } from "@/hooks/useStreamEvents";
import { subscribeToEvents } from "@/lib/events";
import { getErrorMessage } from "@/lib/error-handler";
import { getStatusLabel } from "@/lib/app-status";
import { getServiceSpec } from "@/lib/deployment-utils";
import { Card, CardContent } from "@/components/ui/card";
import { Slider } from "@/components/ui/slider";
import { Input } from "@/components/ui/input";
import {
	DeploymentPhase,
	type Deployment,
} from "@/gen/loco/deployment/v1/deployment_pb";
import { createDeployment } from "@/gen/loco/deployment/v1";
import {
	scaleResource,
	updateResource,
	deleteResource,
} from "@/gen/loco/resource/v1";
import { updateResourceDomain } from "@/gen/loco/domain/v1";
import type { ResourceDomain } from "@/gen/loco/domain/v1/domain_pb";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useMutation } from "@connectrpc/connect-query";
import Loader from "@/assets/loader.svg?react";
import { useState, useEffect } from "react";
import { useParams, useNavigate } from "react-router";
import { toast } from "sonner";

// ─── Design tokens ────────────────────────────────────────────────────────────

const STATUS_CFG: Record<string, { dot: string; color: string; bg: string; label: string }> = {
	healthy:   { dot: "#4a7c59", color: "#3a6b4a", bg: "#eaf2ed", label: "Healthy" },
	deploying: { dot: "#5b7ec0", color: "#3a5298", bg: "#e8edf8", label: "Deploying" },
	degraded:  { dot: "#d4870a", color: "#9c6b1e", bg: "#fdf3e3", label: "Degraded" },
	failed:    { dot: "#c0392b", color: "#8b2e2e", bg: "#fdeaea", label: "Unavailable" },
	suspended: { dot: "#b0a090", color: "#7a6a58", bg: "#f0ece6", label: "Suspended" },
	pending:   { dot: "#b0a090", color: "#7a6a58", bg: "#f0ece6", label: "Pending" },
};

const PHASE_CFG: Record<DeploymentPhase, { label: string; bg: string; color: string }> = {
	[DeploymentPhase.UNSPECIFIED]: { label: "Unknown",   bg: "#ede7dd", color: "#7a6a58" },
	[DeploymentPhase.PENDING]:     { label: "Pending",   bg: "#ede7dd", color: "#7a6a58" },
	[DeploymentPhase.DEPLOYING]:   { label: "Deploying", bg: "#e8edf8", color: "#3a5298" },
	[DeploymentPhase.RUNNING]:     { label: "Running",   bg: "#eaf2ed", color: "#3a6b4a" },
	[DeploymentPhase.SUCCEEDED]:   { label: "Succeeded", bg: "#eaf2ed", color: "#3a6b4a" },
	[DeploymentPhase.FAILED]:      { label: "Failed",    bg: "#fdeaea", color: "#8b2e2e" },
	[DeploymentPhase.CANCELED]:    { label: "Canceled",  bg: "#f0ece6", color: "#8a7a68" },
};

const NODE_STYLE: Record<string, { border: string; bg: string; text: string }> = {
	self:     { border: "#c4956a", bg: "#fdf6ee", text: "#3d2a14" },
	service:  { border: "#c0b8ac", bg: "#faf7f2", text: "#4a3c30" },
	gateway:  { border: "#8fa8c8", bg: "#f0f4fa", text: "#2a3e5c" },
	database: { border: "#8fbfa8", bg: "#f0f8f4", text: "#1e4a38" },
	cache:    { border: "#b8a0c8", bg: "#f6f0fa", text: "#3c2460" },
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
		if (typeof timestamp === "object" && timestamp !== null && "seconds" in timestamp) {
			ms = Number((timestamp as Record<string, unknown>).seconds) * 1000;
		} else if (typeof timestamp === "number") {
			ms = timestamp;
		} else {
			return "—";
		}
		const diff = Date.now() - ms;
		const mins = Math.floor(diff / 60_000);
		const hrs  = Math.floor(diff / 3_600_000);
		const days = Math.floor(diff / 86_400_000);
		if (mins < 1)  return "Just now";
		if (mins < 60) return `${mins}m ago`;
		if (hrs < 24)  return `${hrs}h ago`;
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
	if (statusKey === "failed")   return 88 + (base % 10);
	return 28 + (base % 38);
}

// ─── Architecture Diagram ─────────────────────────────────────────────────────

interface ArchNode { id: string; label: string; type: string; x: number; y: number; replicas?: number }
interface ArchEdge { from: string; to: string; label: string }

function ArchDiagram({ resourceName }: { resourceName: string }) {
	const [hovered, setHovered] = useState<string | null>(null);

	const NW = 124, NH = 46;

	const nodes: ArchNode[] = [
		{ id: "gateway",  label: "api-gateway",   type: "gateway",  x: 240, y: 36  },
		{ id: "self",     label: resourceName,     type: "self",     x: 240, y: 148, replicas: 3 },
		{ id: "postgres", label: "postgres-db",   type: "database", x: 430, y: 96  },
		{ id: "redis",    label: "redis-cache",   type: "cache",    x: 430, y: 200 },
		{ id: "ext",      label: "External API",  type: "external", x: 50,  y: 148 },
	];

	const edges: ArchEdge[] = [
		{ from: "gateway", to: "self",     label: "routes traffic"  },
		{ from: "ext",     to: "self",     label: "calls inbound"   },
		{ from: "self",    to: "postgres", label: "reads / writes"  },
		{ from: "self",    to: "redis",    label: "session cache"   },
	];

	const activeEdges = hovered
		? edges.filter(e => e.from === hovered || e.to === hovered)
		: [];

	const edgePath = (edge: ArchEdge) => {
		const f = nodes.find(n => n.id === edge.from);
		const t = nodes.find(n => n.id === edge.to);
		if (!f || !t) return null;
		const fx = f.x + NW / 2, fy = f.y + NH / 2;
		const tx = t.x + NW / 2, ty = t.y + NH / 2;
		const dx = tx - fx, dy = ty - fy;
		let x1: number, y1: number, x2: number, y2: number;
		if (Math.abs(dx) > Math.abs(dy)) {
			x1 = dx > 0 ? f.x + NW : f.x; y1 = fy;
			x2 = dx > 0 ? t.x       : t.x + NW; y2 = ty;
		} else {
			x1 = fx; y1 = dy > 0 ? f.y + NH : f.y;
			x2 = tx; y2 = dy > 0 ? t.y       : t.y + NH;
		}
		const mx = (x1 + x2) / 2, my = (y1 + y2) / 2;
		const d = Math.abs(dx) > Math.abs(dy)
			? `M${x1} ${y1} C${mx} ${y1},${mx} ${y2},${x2} ${y2}`
			: `M${x1} ${y1} C${x1} ${my},${x2} ${my},${x2} ${y2}`;
		return { d, mx, my };
	};

	return (
		<div>
			<svg viewBox="0 0 600 280" style={{ width: "100%", display: "block" }}>
				<defs>
					<marker id="arr"  markerWidth="7" markerHeight="6" refX="7" refY="3" orient="auto"><polygon points="0 0,7 3,0 6" fill="#cec4b8"/></marker>
					<marker id="arrA" markerWidth="7" markerHeight="6" refX="7" refY="3" orient="auto"><polygon points="0 0,7 3,0 6" fill="#c4956a"/></marker>
				</defs>

				{edges.map((edge, i) => {
					const p = edgePath(edge);
					if (!p) return null;
					const active = hovered && (edge.from === hovered || edge.to === hovered);
					return (
						<g key={i}>
							<path d={p.d} fill="none"
								stroke={active ? "#c4956a" : "#ddd5c8"}
								strokeWidth={active ? 1.5 : 1}
								strokeDasharray={active ? "none" : "5 4"}
								markerEnd={active ? "url(#arrA)" : "url(#arr)"}
								style={{ transition: "stroke 0.18s, stroke-width 0.18s" }}
							/>
							{active && (
								<text x={p.mx} y={p.my - 7} textAnchor="middle" fontSize="9.5" fill="#b07840" fontFamily="'DM Mono',monospace">{edge.label}</text>
							)}
						</g>
					);
				})}

				{nodes.map(node => {
					const s = NODE_STYLE[node.type] ?? NODE_STYLE.service;
					const isActive = hovered === node.id || activeEdges.some(e => e.from === node.id || e.to === node.id);
					const isSelf = node.type === "self";
					const subtitles: Record<string, string> = { self: `${node.replicas ?? 1} replicas`, gateway: "ingress", database: "postgresql", cache: "redis", external: "external", service: "service" };
					return (
						<g key={node.id}
							transform={`translate(${node.x},${node.y})`}
							style={{ cursor: "pointer" }}
							onMouseEnter={() => { setHovered(node.id); }}
							onMouseLeave={() => { setHovered(null); }}
						>
							<rect x={0} y={0} width={NW} height={NH} rx={9}
								fill={s.bg}
								stroke={isActive || isSelf ? s.border : "#e2d8cc"}
								strokeWidth={isSelf ? 2.5 : isActive ? 1.5 : 1}
								style={{ transition: "all 0.15s", filter: isActive ? "drop-shadow(0 3px 10px rgba(0,0,0,0.09))" : "none" }}
							/>
							<text x={NW / 2} y={18} textAnchor="middle" fontSize="11.5"
								fontWeight={isSelf ? "600" : "500"}
								fontFamily="'DM Mono',monospace"
								fill={s.text}
							>{node.label}</text>
							<text x={NW / 2} y={33} textAnchor="middle" fontSize="9"
								fontFamily="'Satoshi',sans-serif"
								fill="#a89880"
							>{subtitles[node.type] ?? ""}</text>
							{node.type !== "external" && (
								<circle cx={NW - 9} cy={9} r={4} fill="#4a7c59" />
							)}
						</g>
					);
				})}
			</svg>

			<div style={{ padding: "10px 20px 14px", borderTop: "1px solid #ede7dd", display: "flex", gap: "14px", flexWrap: "wrap", alignItems: "center" }}>
				{([["self","This service"],["gateway","Gateway"],["database","Database"],["cache","Cache"],["external","External"]] as [string,string][]).map(([t,l]) => (
					<div key={t} style={{ display: "flex", alignItems: "center", gap: "5px" }}>
						<div style={{ width: "9px", height: "9px", borderRadius: "3px", background: NODE_STYLE[t].bg, border: `1.5px solid ${NODE_STYLE[t].border}` }}/>
						<span style={{ fontSize: "10.5px", color: "#9a8a78", fontFamily: "'Satoshi',sans-serif" }}>{l}</span>
					</div>
				))}
				<span style={{ marginLeft: "auto", fontSize: "10.5px", color: "#b8a898", fontFamily: "'Satoshi',sans-serif", fontStyle: "italic" }}>Hover to explore connections · illustrative</span>
			</div>
		</div>
	);
}

// ─── Spec Diff Modal ──────────────────────────────────────────────────────────

interface DiffRow { key: string; label: string; current: string; old: string; changed: boolean }

function buildDiff(current: Deployment, old: Deployment): DiffRow[] {
	const rows: DiffRow[] = [];
	const cs = getServiceSpec(current);
	const os = getServiceSpec(old);
	if (!cs || !os) return rows;

	const row = (key: string, label: string, cv: unknown, ov: unknown) => {
		const c = String(cv ?? "—"), o = String(ov ?? "—");
		rows.push({ key, label, current: c, old: o, changed: c !== o });
	};

	row("image",        "Image",        cs.build?.image,   os.build?.image);
	row("cpu",          "CPU limit",    cs.cpu,            os.cpu);
	row("memory",       "Memory limit", cs.memory,         os.memory);
	row("minReplicas",  "Min replicas", cs.minReplicas,    os.minReplicas);
	row("maxReplicas",  "Max replicas", cs.maxReplicas,    os.maxReplicas);
	row("port",         "Port",         cs.port,           os.port);

	const SENSITIVE = ["DATABASE_URL", "STRIPE_KEY", "REDIS_URL", "SECRET", "PASSWORD", "TOKEN"];
	const csEnv = cs.env, osEnv = os.env;
	const allKeys = new Set([...Object.keys(csEnv), ...Object.keys(osEnv)]);
	allKeys.forEach(k => {
		const cv = csEnv[k], ov = osEnv[k];
		if (cv !== ov) {
			const mask = SENSITIVE.some(s => k.toUpperCase().includes(s));
			rows.push({ key: `env_${k}`, label: `env.${k}`, current: cv !== undefined ? (mask ? "••••••" : cv) : "—", old: ov !== undefined ? (mask ? "••••••" : ov) : "—", changed: true });
		}
	});
	return rows;
}

function SpecDiffModal({ current, old, onClose }: { current: Deployment; old: Deployment; onClose: () => void }) {
	const rows    = buildDiff(current, old);
	const changed = rows.filter(r => r.changed);
	const same    = rows.filter(r => !r.changed);

	return (
		<div style={{ position: "fixed", inset: 0, zIndex: 50, display: "flex", alignItems: "center", justifyContent: "center", background: "rgba(42,32,24,0.3)", backdropFilter: "blur(3px)" }} onClick={onClose}>
			<div style={{ background: "#faf7f2", borderRadius: "16px", border: "1px solid #e0d8cc", boxShadow: "0 20px 60px rgba(42,32,24,0.2)", width: "min(640px, calc(100vw - 48px))", maxHeight: "80vh", overflow: "hidden", display: "flex", flexDirection: "column" }} onClick={e => { e.stopPropagation(); }}>
				<div style={{ padding: "20px 24px 16px", borderBottom: "1px solid #ede7dd", display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: "16px" }}>
					<div>
						<div style={{ fontFamily: "'DM Serif Display',serif", fontSize: "18px", marginBottom: "6px" }}>Spec diff</div>
						<div style={{ fontSize: "12px", color: "#a0907e", display: "flex", gap: "8px", alignItems: "center", flexWrap: "wrap" }}>
							<span style={{ fontFamily: "'DM Mono',monospace", background: "#ede7dd", padding: "2px 7px", borderRadius: "4px" }}>{relativeTime(old.createdAt)}</span>
							<span style={{ color: "#c4956a" }}>→</span>
							<span style={{ fontFamily: "'DM Mono',monospace", background: "#eaf2ed", color: "#3a6b4a", padding: "2px 7px", borderRadius: "4px" }}>{relativeTime(current.createdAt)} (current)</span>
						</div>
					</div>
					<button onClick={onClose} style={{ background: "none", border: "none", cursor: "pointer", color: "#8a7a68", padding: "4px", flexShrink: 0 }}>
						<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
					</button>
				</div>
				<div style={{ overflowY: "auto", padding: "16px 24px 24px" }}>
					{changed.length === 0 ? (
						<div style={{ textAlign: "center", padding: "32px", color: "#a0907e", fontSize: "14px" }}>No spec changes between these deployments.</div>
					) : (
						<>
							<div style={{ fontSize: "11px", fontWeight: 600, color: "#8b2e2e", letterSpacing: "0.07em", textTransform: "uppercase", marginBottom: "8px" }}>{changed.length} change{changed.length !== 1 ? "s" : ""}</div>
							<div style={{ display: "flex", flexDirection: "column", gap: "4px", marginBottom: "20px" }}>
								{changed.map(r => (
									<div key={r.key} style={{ display: "grid", gridTemplateColumns: "140px 1fr 1fr", gap: "8px", padding: "9px 12px", background: "#fff8f0", borderRadius: "8px", border: "1px solid #f0ddc8", alignItems: "start" }}>
										<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#8a7a68", paddingTop: "14px" }}>{r.label}</span>
										<div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
											<span style={{ fontSize: "9px", color: "#c0392b", fontWeight: 600, letterSpacing: "0.06em", textTransform: "uppercase" }}>Before</span>
											<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#8b2e2e", background: "#fdeaea", padding: "3px 7px", borderRadius: "5px", wordBreak: "break-all" }}>{r.old}</span>
										</div>
										<div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
											<span style={{ fontSize: "9px", color: "#3a6b4a", fontWeight: 600, letterSpacing: "0.06em", textTransform: "uppercase" }}>After</span>
											<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#3a6b4a", background: "#eaf2ed", padding: "3px 7px", borderRadius: "5px", wordBreak: "break-all" }}>{r.current}</span>
										</div>
									</div>
								))}
							</div>
						</>
					)}
					{same.length > 0 && (
						<>
							<div style={{ fontSize: "11px", fontWeight: 600, color: "#a0907e", letterSpacing: "0.07em", textTransform: "uppercase", marginBottom: "8px" }}>Unchanged</div>
							<div style={{ display: "flex", flexDirection: "column", gap: "3px" }}>
								{same.map(r => (
									<div key={r.key} style={{ display: "grid", gridTemplateColumns: "140px 1fr", gap: "8px", padding: "7px 12px", borderRadius: "7px", alignItems: "center" }}>
										<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#b0a090" }}>{r.label}</span>
										<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#8a7a68" }}>{r.current}</span>
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

function ActivitySheet({ open, onClose, resourceId }: { open: boolean; onClose: () => void; resourceId: string }) {
	const { events } = useStreamEvents(resourceId);
	const [filter, setFilter] = useState<"all" | "warning" | "normal">("all");

	const filtered = filter === "all" ? events
		: filter === "warning" ? events.filter(e => e.severity === "Warning")
		: events.filter(e => e.severity === "Normal");

	const filters = [
		{ id: "all" as const,     label: "All" },
		{ id: "warning" as const, label: "Warnings" },
		{ id: "normal" as const,  label: "Normal" },
	];

	return (
		<>
			<div style={{ position: "fixed", inset: 0, background: "rgba(42,32,24,0.12)", zIndex: 40, opacity: open ? 1 : 0, pointerEvents: open ? "all" : "none", transition: "opacity 0.28s ease" }} onClick={onClose} />
			<div style={{ position: "fixed", top: 0, right: 0, bottom: 0, width: "min(420px, 100vw)", background: "#faf7f2", borderLeft: "1px solid #e8e0d4", zIndex: 50, display: "flex", flexDirection: "column", boxShadow: "-8px 0 40px rgba(42,32,24,0.12)", transform: open ? "translateX(0)" : "translateX(100%)", transition: "transform 0.32s cubic-bezier(0.32,0.72,0,1)" }}>
				<div style={{ padding: "20px 22px 0", borderBottom: "1px solid #e8e0d4" }}>
					<div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "14px" }}>
						<span style={{ fontFamily: "'DM Serif Display',serif", fontSize: "18px" }}>Activity</span>
						<button onClick={onClose} style={{ background: "none", border: "none", cursor: "pointer", color: "#8a7a68", padding: "4px", borderRadius: "6px" }}>
							<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
						</button>
					</div>
					<div style={{ display: "flex", gap: "2px", marginBottom: "-1px" }}>
						{filters.map(f => (
							<button key={f.id} onClick={() => { setFilter(f.id); }} style={{ background: "none", border: "none", cursor: "pointer", padding: "7px 12px", fontSize: "12px", fontWeight: 500, fontFamily: "'Satoshi',sans-serif", color: filter === f.id ? "#2a2018" : "#8a7a68", borderBottom: filter === f.id ? "2px solid #c4956a" : "2px solid transparent", transition: "all 0.14s" }}>{f.label}</button>
						))}
					</div>
				</div>
				<div style={{ flex: 1, overflowY: "auto", padding: "16px 22px" }}>
					{filtered.length === 0 ? (
						<div style={{ textAlign: "center", padding: "40px 0", color: "#a0907e", fontSize: "13px" }}>No events yet</div>
					) : (
						<div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
							{filtered.map((ev, i) => {
								const warn = ev.severity === "Warning";
								const cfg = warn ? { icon: "!", bg: "#fdf3e3", color: "#9c6b1e", dot: "#d4870a" } : { icon: "✓", bg: "#eaf2ed", color: "#3a6b4a", dot: "#4a7c59" };
								return (
									<div key={i} style={{ display: "flex", gap: "12px", padding: "10px 12px", borderRadius: "10px", transition: "background 0.12s" }} onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.background = "#f2ece2"; }} onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = "transparent"; }}>
										<div style={{ width: "28px", height: "28px", borderRadius: "8px", flexShrink: 0, background: cfg.bg, border: `1px solid ${cfg.dot}30`, display: "flex", alignItems: "center", justifyContent: "center", fontSize: "12px", color: cfg.color, fontWeight: 700, marginTop: "1px" }}>{cfg.icon}</div>
										<div style={{ flex: 1, minWidth: 0 }}>
											<div style={{ fontSize: "13px", fontWeight: 500, color: "#2a2018", marginBottom: "2px", lineHeight: 1.3 }}>{ev.eventType}</div>
											<div style={{ fontSize: "11px", color: "#8a7a68", marginBottom: "5px", lineHeight: 1.4 }}>{ev.message}</div>
											<div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
												<span style={{ fontSize: "10px", color: "#b0a090", fontFamily: "'DM Mono',monospace" }}>{new Date(ev.timestamp).toLocaleTimeString()}</span>
												{ev.pod && <span style={{ fontSize: "10px", background: "#ede7dd", color: "#8a7a68", padding: "1px 6px", borderRadius: "4px", fontFamily: "'DM Mono',monospace" }}>{ev.pod}</span>}
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

const CPU_OPTIONS = ["100m","250m","500m","750m","1000m","1250m","1500m","1750m","2000m"];
const MEM_OPTIONS = ["256Mi","512Mi","768Mi","1Gi","1.25Gi","1.5Gi","2Gi"];

interface SettingsSheetProps {
	open: boolean;
	onClose: () => void;
	resourceId: string;
	resourceName: string;
	domains: ResourceDomain[];
	activeDep: Deployment | undefined;
}

function SettingsSheet({ open, onClose, resourceId, resourceName: initialName, domains, activeDep }: SettingsSheetProps) {
	const navigate = useNavigate();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();

	// Scale state
	const svc = activeDep ? getServiceSpec(activeDep) : undefined;
	const initCpuIdx = svc?.cpu ? CPU_OPTIONS.indexOf(svc.cpu) : -1;
	const initMemIdx = svc?.memory ? MEM_OPTIONS.indexOf(svc.memory) : -1;
	const [cpuIndex,    setCpuIndex]    = useState(initCpuIdx >= 0 ? initCpuIdx : 4);
	const [memoryIndex, setMemoryIndex] = useState(initMemIdx >= 0 ? initMemIdx : 1);
	const [replicas,    setReplicas]    = useState(activeDep?.replicas ?? 1);

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
	useEffect(() => { if (open) setName(initialName); }, [open, initialName]);

	// Domain state — editable subdomain
	const primaryDomain = domains[0];
	const domainStr = primaryDomain?.domain ?? "";
	const dotIdx = domainStr.indexOf(".");
	const initSubdomain = dotIdx > -1 ? domainStr.slice(0, dotIdx) : domainStr;
	const domainSuffix  = dotIdx > -1 ? domainStr.slice(dotIdx) : "";
	const [subdomain, setSubdomain] = useState(initSubdomain);
	useEffect(() => {
		if (!open) return;
		const d = domains[0]?.domain ?? "";
		const di = d.indexOf(".");
		setSubdomain(di > -1 ? d.slice(0, di) : d);
	}, [open, domains]);

	// Danger zone
	const [confirmDelete, setConfirmDelete] = useState(false);

	const { mutate: scale,        isPending: scaling  } = useMutation(scaleResource);
	const { mutate: update,       isPending: updating } = useMutation(updateResource);
	const { mutate: del,          isPending: deleting  } = useMutation(deleteResource);
	const { mutate: updateDomain, isPending: savingDomain } = useMutation(updateResourceDomain);

	const handleScale = () => {
		scale(
			{ resourceId, replicas, cpu: CPU_OPTIONS[cpuIndex], memory: MEM_OPTIONS[memoryIndex] },
			{ onSuccess: () => { toast.success("Scaling applied"); }, onError: () => { toast.error("Failed to scale"); } },
		);
	};

	const handleNameSave = () => {
		if (!name.trim() || name.trim() === initialName) return;
		update(
			{ resourceId, name: name.trim() },
			{ onSuccess: () => { toast.success("Resource renamed"); }, onError: () => { toast.error("Failed to rename"); } },
		);
	};

	const handleDomainSave = () => {
		if (!primaryDomain?.id || !subdomain.trim() || subdomain.trim() === initSubdomain) return;
		const newDomain = `${subdomain.trim()}${domainSuffix}`;
		updateDomain(
			// eslint-disable-next-line @typescript-eslint/no-explicit-any
			{ domainId: primaryDomain.id, domain: newDomain } as any,
			{ onSuccess: () => { toast.success("Domain updated"); }, onError: () => { toast.error("Failed to update domain"); } },
		);
	};

	const handleDelete = () => {
		del(
			{ resourceId },
			{
				onSuccess: () => {
					toast.success("Resource deleted");
					onClose();
					if (activeOrgId && activeWorkspaceId) void navigate(`/org/${activeOrgId}/wks/${activeWorkspaceId}`);
				},
				onError: () => { toast.error("Failed to delete resource"); },
			},
		);
	};

	const sec = (title: string) => (
		<div style={{ fontSize: "10px", fontWeight: 700, color: "#b0a090", letterSpacing: "0.08em", textTransform: "uppercase", marginBottom: "12px" }}>{title}</div>
	);
	const div = <div style={{ borderTop: "1px solid #ede7dd", margin: "24px 0" }} />;

	return (
		<>
			<div style={{ position: "fixed", inset: 0, background: "rgba(42,32,24,0.12)", zIndex: 40, opacity: open ? 1 : 0, pointerEvents: open ? "all" : "none", transition: "opacity 0.28s ease" }} onClick={onClose} />
			<div style={{ position: "fixed", top: 0, right: 0, bottom: 0, width: "min(440px, 100vw)", background: "#faf7f2", borderLeft: "1px solid #e8e0d4", zIndex: 50, display: "flex", flexDirection: "column", boxShadow: "-8px 0 40px rgba(42,32,24,0.12)", transform: open ? "translateX(0)" : "translateX(100%)", transition: "transform 0.32s cubic-bezier(0.32,0.72,0,1)" }}>
				{/* header */}
				<div style={{ padding: "20px 22px 16px", borderBottom: "1px solid #e8e0d4", display: "flex", alignItems: "center", justifyContent: "space-between" }}>
					<span style={{ fontFamily: "'DM Serif Display',serif", fontSize: "18px" }}>Settings</span>
					<button onClick={onClose} style={{ background: "none", border: "none", cursor: "pointer", color: "#8a7a68", padding: "4px", borderRadius: "6px" }}>
						<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
					</button>
				</div>

				{/* body */}
				<div style={{ flex: 1, overflowY: "auto", padding: "24px 22px" }}>

					{/* Scale */}
					{sec("Scale")}
					<div style={{ display: "flex", flexDirection: "column", gap: "20px" }}>
						<div>
							<div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "8px" }}>
								<span style={{ fontSize: "13px", color: "#4a3c30", fontWeight: 500 }}>Replicas</span>
								<span style={{ fontSize: "13px", fontWeight: 600, color: "#2a2018" }}>{replicas}</span>
							</div>
							<Input type="number" min="1" max="20" value={replicas} onChange={e => { const n = parseInt(e.target.value, 10); if (!isNaN(n) && n > 0) setReplicas(n); }} className="w-24 text-center" style={{ fontFamily: "'DM Mono',monospace", fontSize: "13px" }} />
						</div>
						<div>
							<div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "10px" }}>
								<span style={{ fontSize: "13px", color: "#4a3c30", fontWeight: 500 }}>CPU</span>
								<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "12px", fontWeight: 600, color: "#2a2018", background: "#ede7dd", padding: "2px 8px", borderRadius: "5px" }}>{CPU_OPTIONS[cpuIndex]}</span>
							</div>
							<Slider value={[cpuIndex]} onValueChange={v => { setCpuIndex(v[0]); }} min={0} max={CPU_OPTIONS.length - 1} step={1} className="w-full" />
							<div style={{ display: "flex", justifyContent: "space-between", marginTop: "6px" }}>
								<span style={{ fontSize: "10px", color: "#b0a090" }}>100m</span>
								<span style={{ fontSize: "10px", color: "#b0a090" }}>2000m</span>
							</div>
						</div>
						<div>
							<div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "10px" }}>
								<span style={{ fontSize: "13px", color: "#4a3c30", fontWeight: 500 }}>Memory</span>
								<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "12px", fontWeight: 600, color: "#2a2018", background: "#ede7dd", padding: "2px 8px", borderRadius: "5px" }}>{MEM_OPTIONS[memoryIndex]}</span>
							</div>
							<Slider value={[memoryIndex]} onValueChange={v => { setMemoryIndex(v[0]); }} min={0} max={MEM_OPTIONS.length - 1} step={1} className="w-full" />
							<div style={{ display: "flex", justifyContent: "space-between", marginTop: "6px" }}>
								<span style={{ fontSize: "10px", color: "#b0a090" }}>256Mi</span>
								<span style={{ fontSize: "10px", color: "#b0a090" }}>2Gi</span>
							</div>
						</div>
						<button onClick={handleScale} disabled={scaling} style={{ alignSelf: "flex-start", display: "inline-flex", alignItems: "center", gap: "6px", padding: "8px 16px", borderRadius: "8px", background: "#2a2018", color: "#f7f3ec", border: "none", cursor: scaling ? "not-allowed" : "pointer", opacity: scaling ? 0.6 : 1, fontSize: "13px", fontWeight: 500, fontFamily: "'Satoshi',sans-serif" }}>
							{scaling && <svg className="rdspin" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>}
							{scaling ? "Applying…" : "Apply"}
						</button>
					</div>

					{div}

					{/* Domain */}
					{sec("Domain")}
					{primaryDomain ? (
						<div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
							<div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
								<Input
									value={subdomain}
									onChange={e => { setSubdomain(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "")); }}
									placeholder="subdomain"
									style={{ fontFamily: "'DM Mono',monospace", fontSize: "12px", flex: 1 }}
								/>
								{domainSuffix && (
									<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "12px", color: "#a0907e", background: "#f0ebe3", padding: "8px 10px", borderRadius: "7px", border: "1px solid #e0d8cc", whiteSpace: "nowrap", flexShrink: 0 }}>
										{domainSuffix}
									</span>
								)}
							</div>
							<div style={{ display: "flex", gap: "8px", alignItems: "center" }}>
								<a href={`https://${domainStr}`} target="_blank" rel="noopener noreferrer" style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#4a6b9c", textDecoration: "none", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
									↗ {subdomain || "…"}{domainSuffix}
								</a>
								<button onClick={handleDomainSave} disabled={savingDomain || !subdomain.trim() || subdomain.trim() === initSubdomain} style={{ display: "inline-flex", alignItems: "center", gap: "5px", padding: "6px 12px", borderRadius: "7px", background: "transparent", color: "#4a3c30", border: "1px solid #ddd5c8", cursor: "pointer", fontSize: "12px", fontFamily: "'Satoshi',sans-serif", whiteSpace: "nowrap", opacity: (savingDomain || !subdomain.trim() || subdomain.trim() === initSubdomain) ? 0.5 : 1 }}>
									{savingDomain ? "Saving…" : "Save"}
								</button>
							</div>
						</div>
					) : (
						<div style={{ fontSize: "13px", color: "#a0907e" }}>No domain configured</div>
					)}

					{div}

					{/* General */}
					{sec("General")}
					<div style={{ display: "flex", gap: "8px", alignItems: "center" }}>
						<Input value={name} onChange={e => { setName(e.target.value); }} placeholder="Resource name" style={{ fontFamily: "'Satoshi',sans-serif", fontSize: "13px", flex: 1 }} />
						<button onClick={handleNameSave} disabled={updating || !name.trim() || name.trim() === initialName} style={{ display: "inline-flex", alignItems: "center", gap: "5px", padding: "8px 14px", borderRadius: "8px", background: "transparent", color: "#4a3c30", border: "1px solid #ddd5c8", cursor: "pointer", fontSize: "13px", fontWeight: 500, fontFamily: "'Satoshi',sans-serif", whiteSpace: "nowrap", opacity: (updating || !name.trim() || name.trim() === initialName) ? 0.5 : 1 }}>
							{updating ? "Saving…" : "Save"}
						</button>
					</div>

					{div}

					{/* Danger zone */}
					{sec("Danger Zone")}
					{!confirmDelete ? (
						<button onClick={() => { setConfirmDelete(true); }} style={{ display: "inline-flex", alignItems: "center", gap: "6px", padding: "8px 14px", borderRadius: "8px", background: "#fdeaea", color: "#8b2e2e", border: "1px solid #f0c8c8", cursor: "pointer", fontSize: "13px", fontWeight: 500, fontFamily: "'Satoshi',sans-serif" }}>
							<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6M14 11v6M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
							Delete resource
						</button>
					) : (
						<div style={{ padding: "14px", background: "#fdeaea", border: "1px solid #f0c8c8", borderRadius: "10px" }}>
							<p style={{ fontSize: "13px", color: "#8b2e2e", marginBottom: "12px", fontWeight: 500 }}>Are you sure? This cannot be undone.</p>
							<div style={{ display: "flex", gap: "8px" }}>
								<button onClick={handleDelete} disabled={deleting} style={{ display: "inline-flex", alignItems: "center", gap: "5px", padding: "7px 14px", borderRadius: "7px", background: "#8b2e2e", color: "#fff", border: "none", cursor: deleting ? "not-allowed" : "pointer", fontSize: "12px", fontWeight: 600, fontFamily: "'Satoshi',sans-serif", opacity: deleting ? 0.7 : 1 }}>
									{deleting ? "Deleting…" : "Yes, delete"}
								</button>
								<button onClick={() => { setConfirmDelete(false); }} style={{ padding: "7px 12px", borderRadius: "7px", background: "transparent", color: "#6b5d4f", border: "1px solid #ddd5c8", cursor: "pointer", fontSize: "12px", fontFamily: "'Satoshi',sans-serif" }}>Cancel</button>
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
	const { resourceId }                     = useParams<{ resourceId: string }>();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const navigate                           = useNavigate();

	const { resource: resourceResponse, deployments, isLoading, error } =
		useResourceDetails(resourceId ?? "");
	const resource = resourceResponse?.resource;

	const [diff, setDiff]               = useState<{ current: Deployment; old: Deployment } | null>(null);
	const [activityOpen, setActivityOpen] = useState(false);
	const [settingsOpen, setSettingsOpen] = useState(false);

	const anySheetOpen = activityOpen || settingsOpen;

	const redeployMutation = useMutation(createDeployment);

	useEffect(() => {
		if (!resourceId) return;
		const unsub = subscribeToEvents(`resource:${resourceId}`, () => {/* no-op */});
		return unsub;
	}, [resourceId]);

	// ── loading / error states ───────────────────────────────────────────────
	if (!resourceId) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md"><CardContent className="p-6 text-center">
					<p className="text-destructive font-medium mb-2">Invalid Resource ID</p>
					<p className="text-sm text-muted-foreground">The resource ID is missing from the URL</p>
				</CardContent></Card>
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
				<Card className="max-w-md"><CardContent className="p-6 text-center">
					<p className="text-destructive font-medium mb-4">Error Loading Resource</p>
					<p className="text-sm text-muted-foreground">{getErrorMessage(error, "Failed to load resource")}</p>
				</CardContent></Card>
			</div>
		);
	}

	if (!resource) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md"><CardContent className="p-6 text-center">
					<p className="text-destructive font-medium mb-2">Resource Not Found</p>
					<p className="text-sm text-muted-foreground">The resource with ID {resourceId} does not exist</p>
				</CardContent></Card>
			</div>
		);
	}

	// ── derived data ─────────────────────────────────────────────────────────
	const statusKey     = statusKeyFromLabel(getStatusLabel(resource.status));
	const st            = STATUS_CFG[statusKey] ?? STATUS_CFG.pending;
	const activeDep     = deployments.find(d => d.isActive) ?? deployments[0];
	const primaryDomain = resource.domains?.[0]?.domain;
	const activeSvc     = activeDep ? getServiceSpec(activeDep) : undefined;

	const handleRedeploy = async () => {
		if (!activeDep?.spec) { toast.error("No active deployment to redeploy"); return; }
		try {
			await redeployMutation.mutateAsync({ resourceId, region: activeDep.region, spec: activeDep.spec, environmentId: activeDep.environmentId });
			toast.success("Redeployment started");
		} catch {
			toast.error("Failed to trigger redeployment");
		}
	};

	const redeploying = redeployMutation.isPending;

	// ── per-region card data ─────────────────────────────────────────────────
	const regionCards = resource.regions.map((r, idx) => {
		const rDep = deployments.find(d => d.region === r.region && d.isActive) ?? activeDep;
		const rSvc = rDep ? getServiceSpec(rDep) : activeSvc;
		const statusKey2 = (() => {
			switch (r.status) {
				case 1: return "deploying";
				case 2: return "healthy";
				case 3: return "degraded";
				case 4: return "failed";
				default: return "pending";
			}
		})();
		const rs = STATUS_CFG[statusKey2] ?? STATUS_CFG.pending;

		const cpuLimit = parseCpuMilli(rSvc?.cpu ?? "500m");
		const memLimit = parseMemMi(rSvc?.memory ?? "512Mi");
		const cpuPct   = mockUsagePct(statusKey2, idx * 3);
		const memPct   = mockUsagePct(statusKey2, idx * 3 + 1);
		const cpuUsed  = Math.round(cpuLimit * cpuPct / 100);
		const memUsed  = Math.round(memLimit * memPct / 100);
		const replicas = rDep?.replicas ?? rSvc?.minReplicas ?? 1;

		return { region: r.region, isPrimary: r.isPrimary, rs, statusKey2, cpuUsed, cpuLimit, memUsed, memLimit, memPct, cpuPct, replicas };
	});

	// ── render ───────────────────────────────────────────────────────────────
	return (
		<div style={{ fontFamily: "'Satoshi', sans-serif", color: "#2a2018", paddingRight: anySheetOpen ? "440px" : "0", transition: "padding-right 0.32s cubic-bezier(0.32,0.72,0,1)" }}>
			<style>{`
				.rd-card { background: #faf7f2; border: 1px solid #e8e0d4; border-radius: 12px; }
				.rd-btn-primary { display:inline-flex;align-items:center;gap:6px;padding:7px 14px;border-radius:8px;font-family:'Satoshi',sans-serif;font-size:13px;font-weight:500;cursor:pointer;transition:all .15s;border:none;background:#2a2018;color:#f7f3ec; }
				.rd-btn-primary:hover:not(:disabled){background:#3d2f20;}
				.rd-btn-primary:disabled{opacity:0.55;cursor:not-allowed;}
				.rd-btn-ghost { display:inline-flex;align-items:center;gap:6px;padding:7px 14px;border-radius:8px;font-family:'Satoshi',sans-serif;font-size:13px;font-weight:500;cursor:pointer;transition:all .15s;background:transparent;color:#6b5d4f;border:1px solid #ddd5c8; }
				.rd-btn-ghost:hover{background:#f0ebe3;border-color:#c9bbad;}
				.rd-btn-icon { display:inline-flex;align-items:center;justify-content:center;width:34px;height:34px;border-radius:8px;cursor:pointer;transition:all .15s;background:transparent;color:#6b5d4f;border:1px solid #ddd5c8; }
				.rd-btn-icon:hover{background:#f0ebe3;border-color:#c9bbad;}
				.rd-btn-icon.active{background:#f0ebe3;border-color:#c9bbad;color:#2a2018;}
				.dep-row { padding:12px 14px;border-radius:10px;transition:background .12s; }
				.dep-row:hover { background:#f2ece2; }
				.dep-row:hover .diff-hint { opacity:1; }
				.diff-hint { opacity:0;transition:opacity .15s;font-size:11px;color:#c4956a;font-weight:500; }
				.epulse { animation:rdep 2.2s ease-in-out infinite; }
				@keyframes rdep { 0%,100%{opacity:1}50%{opacity:.4} }
				.rdspin { animation:rds .9s linear infinite; }
				@keyframes rds { to{transform:rotate(360deg)} }
				.mbar-bg{height:3px;background:#e8e0d4;border-radius:2px;overflow:hidden;}
				.mbar-fill{height:100%;border-radius:2px;transition:width .6s ease;}
			`}</style>

			<div style={{ maxWidth: "1280px", margin: "0 auto", padding: anySheetOpen ? "28px 16px 80px" : "28px 40px 80px", transition: "padding 0.32s cubic-bezier(0.32,0.72,0,1)" }}>

				{/* ── Header ── */}
				<div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", flexWrap: "wrap", gap: "16px", marginBottom: "28px" }}>
					<div>
						<div style={{ display: "flex", alignItems: "center", gap: "10px", marginBottom: "8px" }}>
							<h1 style={{ fontFamily: "'DM Serif Display',serif", fontSize: "28px", fontWeight: 400, letterSpacing: "-0.3px", lineHeight: 1.2, margin: 0 }}>
								{resource.name}
							</h1>
							<span style={{ fontSize: "12px", background: st.bg, color: st.color, padding: "3px 10px", borderRadius: "20px", fontWeight: 600, display: "flex", alignItems: "center", gap: "5px", flexShrink: 0 }}>
								<span className={statusKey !== "healthy" ? "epulse" : ""} style={{ width: "6px", height: "6px", borderRadius: "50%", background: st.dot, display: "inline-block" }} />
								{st.label}
							</span>
						</div>
						<div style={{ display: "flex", alignItems: "center", gap: "7px", flexWrap: "wrap" }}>
							{activeDep && (
								<span style={{ display: "flex", alignItems: "center", gap: "4px", fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#8a7a68", background: "#ede7dd", padding: "3px 8px", borderRadius: "5px" }}>
									<svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 9h6M9 12h6M9 15h4"/></svg>
									{shortId(activeDep.id)}
								</span>
							)}
							{activeDep && deploymentImage(activeDep) !== "—" && (
								<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#6258a0", background: "#eeecf8", padding: "3px 8px", borderRadius: "5px", display: "flex", alignItems: "center", gap: "4px" }}>
									<svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><rect x="2" y="7" width="20" height="14" rx="2"/><path d="M16 7V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v2"/></svg>
									{deploymentImage(activeDep).split("/").pop()}
								</span>
							)}
							{primaryDomain && (
								<a href={`https://${primaryDomain}`} target="_blank" rel="noopener noreferrer" style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#4a6b9c", background: "#edf2f8", padding: "3px 8px", borderRadius: "5px", textDecoration: "none", display: "flex", alignItems: "center", gap: "4px" }}>
									<svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
									{primaryDomain}
								</a>
							)}
						</div>
					</div>

					<div style={{ display: "flex", gap: "6px", paddingTop: "4px", alignItems: "center" }}>
						<button className="rd-btn-ghost" style={{ padding: "7px 14px" }} onClick={() => { if (activeOrgId && activeWorkspaceId) void navigate(`/org/${activeOrgId}/wks/${activeWorkspaceId}/observability`); }}>
							<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
							Telemetry
						</button>
						<button className={`rd-btn-icon${activityOpen ? " active" : ""}`} title="Activity" onClick={() => { setActivityOpen(v => !v); setSettingsOpen(false); }}>
							<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>
						</button>
						<button className={`rd-btn-icon${settingsOpen ? " active" : ""}`} title="Settings" onClick={() => { setSettingsOpen(v => !v); setActivityOpen(false); }}>
							<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="12" cy="12" r="3"/><path d="M12 2v2M12 20v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M2 12h2M20 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>
						</button>
						<button className="rd-btn-primary" onClick={() => { void handleRedeploy(); }} disabled={redeploying || !activeDep}>
							{redeploying
								? <svg className="rdspin" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
								: <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>
							}
							{redeploying ? "Deploying…" : "Redeploy"}
						</button>
					</div>
				</div>

				{/* ── Resource cards ── */}
				{regionCards.length === 1 ? (
					/* Single region: expanded 3-card layout */
					(() => {
						const r = regionCards[0];
						return (
							<div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: "12px", marginBottom: "20px" }}>
								{/* Replicas */}
								<div className="rd-card" style={{ padding: "20px" }}>
									<div style={{ fontSize: "11px", fontWeight: 600, color: "#a0907e", letterSpacing: "0.08em", textTransform: "uppercase", marginBottom: "10px" }}>Replicas</div>
									<div style={{ display: "flex", alignItems: "baseline", gap: "4px" }}>
										<span style={{ fontSize: "28px", fontWeight: 600, fontFamily: "'DM Serif Display',serif", color: "#2a2018" }}>{r.replicas}</span>
									</div>
									<div style={{ display: "flex", gap: "5px", marginTop: "10px", flexWrap: "wrap" }}>
										{Array.from({ length: Math.min(r.replicas, 12) }).map((_, i) => (
											<div key={i} style={{ width: "8px", height: "8px", borderRadius: "50%", background: r.rs.dot }} />
										))}
									</div>
								</div>

								{/* CPU */}
								<div className="rd-card" style={{ padding: "20px" }}>
									<div style={{ fontSize: "11px", fontWeight: 600, color: "#a0907e", letterSpacing: "0.08em", textTransform: "uppercase", marginBottom: "10px" }}>CPU</div>
									<div style={{ display: "flex", alignItems: "baseline", gap: "4px" }}>
										<span style={{ fontSize: "28px", fontWeight: 600, fontFamily: "'DM Serif Display',serif", color: r.cpuPct > 80 ? "#8b2e2e" : "#2a2018" }}>{r.cpuUsed}</span>
										<span style={{ fontSize: "14px", color: "#a0907e" }}>m / {r.cpuLimit}m</span>
									</div>
									<div style={{ marginTop: "12px" }}>
										<div className="mbar-bg">
											<div className="mbar-fill" style={{ width: `${r.cpuPct}%`, background: r.cpuPct > 80 ? "#c0392b" : r.cpuPct > 60 ? "#d4870a" : "#c4956a" }} />
										</div>
										<div style={{ fontSize: "11px", color: "#a0907e", marginTop: "4px" }}>{r.cpuPct}% of limit</div>
									</div>
								</div>

								{/* Memory */}
								<div className="rd-card" style={{ padding: "20px" }}>
									<div style={{ fontSize: "11px", fontWeight: 600, color: "#a0907e", letterSpacing: "0.08em", textTransform: "uppercase", marginBottom: "10px" }}>Memory</div>
									<div style={{ display: "flex", alignItems: "baseline", gap: "4px" }}>
										<span style={{ fontSize: "28px", fontWeight: 600, fontFamily: "'DM Serif Display',serif", color: r.memPct > 80 ? "#8b2e2e" : "#2a2018" }}>{r.memUsed}</span>
										<span style={{ fontSize: "14px", color: "#a0907e" }}>MB / {r.memLimit}MB</span>
									</div>
									<div style={{ marginTop: "12px" }}>
										<div className="mbar-bg">
											<div className="mbar-fill" style={{ width: `${r.memPct}%`, background: r.memPct > 80 ? "#c0392b" : r.memPct > 60 ? "#d4870a" : "#4a7c59" }} />
										</div>
										<div style={{ fontSize: "11px", color: "#a0907e", marginTop: "4px" }}>{r.memPct}% of limit</div>
									</div>
								</div>
							</div>
						);
					})()
				) : regionCards.length > 1 ? (
					/* Multi-region: one compact card per region */
					<div style={{ display: "grid", gridTemplateColumns: `repeat(${Math.min(regionCards.length, 4)}, 1fr)`, gap: "10px", marginBottom: "20px" }}>
						{regionCards.map((r) => (
							<div key={r.region} className="rd-card" style={{ padding: "16px 18px", borderColor: r.statusKey2 === "degraded" ? "#e8d4a4" : r.statusKey2 === "failed" ? "#e8c4c4" : "#e8e0d4" }}>
								<div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "12px" }}>
									<div style={{ fontSize: "12px", fontWeight: 600, color: "#4a3c30", display: "flex", alignItems: "center", gap: "5px" }}>
										{r.region}
										{r.isPrimary && <span style={{ fontSize: "9px", color: "#b0a090", fontWeight: 400 }}>primary</span>}
									</div>
									<div style={{ display: "flex", alignItems: "center", gap: "4px" }}>
										<span style={{ width: "5px", height: "5px", borderRadius: "50%", background: r.rs.dot }} />
										<span style={{ fontSize: "11px", color: r.rs.color, fontWeight: 500 }}>{r.rs.label}</span>
									</div>
								</div>
								<div style={{ display: "flex", flexDirection: "column", gap: "10px" }}>
									<div>
										<div style={{ display: "flex", justifyContent: "space-between", marginBottom: "4px" }}>
											<span style={{ fontSize: "10px", color: "#a0907e", textTransform: "uppercase", letterSpacing: "0.07em", fontWeight: 600 }}>CPU</span>
											<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "10px", color: r.cpuPct > 70 ? "#9c6b1e" : "#6b5d4f" }}>{r.cpuUsed}m / {r.cpuLimit}m</span>
										</div>
										<div className="mbar-bg"><div className="mbar-fill" style={{ width: `${r.cpuPct}%`, background: r.cpuPct > 80 ? "#c0392b" : r.cpuPct > 60 ? "#d4870a" : "#c4956a" }} /></div>
									</div>
									<div>
										<div style={{ display: "flex", justifyContent: "space-between", marginBottom: "4px" }}>
											<span style={{ fontSize: "10px", color: "#a0907e", textTransform: "uppercase", letterSpacing: "0.07em", fontWeight: 600 }}>Memory</span>
											<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "10px", color: r.memPct > 70 ? "#9c6b1e" : "#6b5d4f" }}>{r.memUsed}MB / {r.memLimit}MB</span>
										</div>
										<div className="mbar-bg"><div className="mbar-fill" style={{ width: `${r.memPct}%`, background: r.memPct > 80 ? "#c0392b" : r.memPct > 60 ? "#d4870a" : "#4a7c59" }} /></div>
									</div>
									<div style={{ display: "flex", justifyContent: "space-between", paddingTop: "2px" }}>
										<span style={{ fontSize: "10px", color: "#a0907e", textTransform: "uppercase", letterSpacing: "0.07em", fontWeight: 600 }}>Replicas</span>
										<div style={{ display: "flex", gap: "3px", alignItems: "center" }}>
											{Array.from({ length: Math.min(r.replicas, 8) }).map((_, i) => (
												<div key={i} style={{ width: "5px", height: "5px", borderRadius: "50%", background: r.rs.dot }} />
											))}
											<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "10px", color: "#8a7a68", marginLeft: "4px" }}>{r.replicas}</span>
										</div>
									</div>
								</div>
							</div>
						))}
					</div>
				) : null}

				{/* ── Architecture ── */}
				<div className="rd-card" style={{ overflow: "hidden", marginBottom: "20px" }}>
					<div style={{ padding: "16px 20px 12px", borderBottom: "1px solid #ede7dd", display: "flex", alignItems: "center", justifyContent: "space-between" }}>
						<span style={{ fontFamily: "'DM Serif Display',serif", fontSize: "17px" }}>Architecture</span>
						<span style={{ fontSize: "11px", color: "#a0907e" }}>{resource.name}</span>
					</div>
					<ArchDiagram resourceName={resource.name} />
				</div>

				{/* ── Deployments ── */}
				<div className="rd-card" style={{ overflow: "hidden" }}>
					<div style={{ padding: "16px 20px 12px", borderBottom: "1px solid #ede7dd", display: "flex", alignItems: "center", justifyContent: "space-between" }}>
						<span style={{ fontFamily: "'DM Serif Display',serif", fontSize: "17px" }}>Deployments</span>
						<span style={{ fontSize: "11px", color: "#a0907e" }}>Click any past deployment to compare spec</span>
					</div>

					<div style={{ display: "grid", gridTemplateColumns: "minmax(80px,1fr) minmax(120px,2fr) 130px 110px 90px 110px", gap: "10px", padding: "8px 26px", borderBottom: "1px solid #f0e8dc" }}>
						{["ID", "Image", "Status", "Region", "Time", ""].map((h, i) => (
							<span key={i} style={{ fontSize: "10px", fontWeight: 600, color: "#b0a090", textTransform: "uppercase", letterSpacing: "0.07em" }}>{h}</span>
						))}
					</div>

					<div style={{ padding: "6px 8px" }}>
						{deployments.length === 0 ? (
							<div style={{ padding: "28px", textAlign: "center", color: "#a0907e", fontSize: "13px" }}>No deployments yet</div>
						) : (
							deployments.map(dep => {
								const ph      = PHASE_CFG[dep.status] ?? PHASE_CFG[DeploymentPhase.UNSPECIFIED];
								const isCurr  = dep.isActive;
								const canDiff = !isCurr && activeDep && activeDep.id !== dep.id;
								return (
									<div key={dep.id} className="dep-row" onClick={() => { if (canDiff && activeDep) setDiff({ current: activeDep, old: dep }); }} style={{ display: "grid", gridTemplateColumns: "minmax(80px,1fr) minmax(120px,2fr) 130px 110px 90px 110px", gap: "10px", alignItems: "center", cursor: canDiff ? "pointer" : "default", background: isCurr ? "#f7f3ee" : undefined, borderLeft: isCurr ? "3px solid #c4956a" : "3px solid transparent" }}>
										<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#6b5d4f", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{shortId(dep.id)}</span>
										<span style={{ fontFamily: "'DM Mono',monospace", fontSize: "11px", color: "#6b5d4f", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{deploymentImage(dep).split("/").pop() || "—"}</span>
										<span style={{ fontSize: "11px", background: ph.bg, color: ph.color, padding: "3px 9px", borderRadius: "10px", fontWeight: 600, display: "inline-flex", alignItems: "center", gap: "5px", width: "fit-content" }}>
											{dep.status === DeploymentPhase.DEPLOYING && <span className="rdspin" style={{ display: "inline-block", width: "7px", height: "7px", border: "1.5px solid currentColor", borderTopColor: "transparent", borderRadius: "50%" }} />}
											{ph.label}
										</span>
										<span style={{ fontSize: "11px", color: "#8a7a68", fontFamily: "'DM Mono',monospace", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{dep.region || "—"}</span>
										<span style={{ fontSize: "11px", color: "#a0907e" }}>{relativeTime(dep.createdAt)}</span>
										<div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
											{isCurr && <span style={{ fontSize: "10px", color: "#c4956a", fontWeight: 600 }}>current</span>}
											{!isCurr && dep.status === DeploymentPhase.SUCCEEDED && (
												<button className="rd-btn-ghost" style={{ padding: "3px 9px", fontSize: "11px" }} onClick={e => { e.stopPropagation(); }}>Rollback</button>
											)}
											{canDiff && <span className="diff-hint">diff →</span>}
										</div>
									</div>
								);
							})
						)}
					</div>
				</div>
			</div>

			<ActivitySheet open={activityOpen} onClose={() => { setActivityOpen(false); }} resourceId={resourceId} />

			<SettingsSheet
				open={settingsOpen}
				onClose={() => { setSettingsOpen(false); }}
				resourceId={resourceId}
				resourceName={resource.name}
				domains={resource.domains ?? []}
				activeDep={activeDep}
			/>

			{diff && <SpecDiffModal current={diff.current} old={diff.old} onClose={() => { setDiff(null); }} />}
		</div>
	);
}
