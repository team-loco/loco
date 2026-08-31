import { useState } from "react";

export interface NodeStyle {
	border: string;
	bg: string;
	text: string;
}

export type NodeType =
	| "self"
	| "service"
	| "gateway"
	| "database"
	| "cache"
	| "external";

export const NODE_STYLE: Record<NodeType, NodeStyle> =
	{
		self: { border: "#c4956a", bg: "#fdf6ee", text: "#3d2a14" },
		service: { border: "#c0b8ac", bg: "#faf7f2", text: "#4a3c30" },
		gateway: { border: "#8fa8c8", bg: "#f0f4fa", text: "#2a3e5c" },
		database: { border: "#8fbfa8", bg: "#f0f8f4", text: "#1e4a38" },
		cache: { border: "#b8a0c8", bg: "#f6f0fa", text: "#3c2460" },
		external: { border: "#c0b8ac", bg: "#f5f2ec", text: "#6b5d4f" },
	};

// ─── Helpers ──────────────────────────────────────────────────────────────────

export interface ArchNode {
	id: string;
	label: string;
	type: NodeType;
	x: number;
	y: number;
	replicas?: number;
}
export interface ArchEdge {
	from: string;
	to: string;
	label: string;
}

export function ArchDiagram({ resourceName }: { resourceName: string }) {
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
					const s = NODE_STYLE[node.type];
					const isActive =
						hovered === node.id ||
						activeEdges.some((e) => e.from === node.id || e.to === node.id);
					const isSelf = node.type === "self";
					const subtitles: Record<NodeType, string> = {
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
								{subtitles[node.type]}
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
					] as [NodeType, string][]
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

export function ArchModal({
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
