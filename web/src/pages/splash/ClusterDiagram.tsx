import { AppWindow, Cog, Database, Globe, Server, ShieldCheck, type LucideIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import { cn } from "@/lib/utils";

import { BOX, EDGES, pctX, pctY, readyCount, VIEW_H, VIEW_W, type Box, type NodeKey, type PodPhase, type Rollout } from "./cluster";

interface PodDot {
	id: string;
	phase: PodPhase;
}

const WEB_PODS: readonly PodDot[] = [
	{ id: "w1", phase: "ready" },
	{ id: "w2", phase: "ready" },
];
const WORKER_PODS: readonly PodDot[] = [{ id: "k1", phase: "ready" }];
const DB_PODS: readonly PodDot[] = [{ id: "d1", phase: "ready" }];

function podClass(phase: PodPhase): string {
	switch (phase) {
		case "ready":
			return "animate-pod-pop border-ok-fg bg-ok-bg";
		case "starting":
			return "animate-pod-starting border-warn bg-warn-bg";
		case "terminating":
			return "animate-pod-pop border-line2 bg-bg3 opacity-50";
	}
}

function PodRow({ pods, className, podClassName }: { pods: readonly PodDot[]; className: string; podClassName: string }) {
	return (
		<div className={cn("flex flex-wrap gap-[5px]", className)}>
			{pods.map((p) => (
				<span
					key={p.id}
					title={`pod ${p.id} · ${p.phase}`}
					className={cn("rounded-[4px] border-[1.5px]", podClassName, podClass(p.phase))}
				/>
			))}
		</div>
	);
}

function CardHead({ icon: Icon, name, sub }: { icon: LucideIcon; name: string; sub: string }) {
	return (
		<div className="flex items-center gap-[9px]">
			<span className="flex size-[26px] shrink-0 items-center justify-center rounded-[7px] bg-bg3 text-foreground">
				<Icon size={15} strokeWidth={1.6} />
			</span>
			<span className="flex min-w-0 flex-col leading-[1.25]">
				<span className="text-[13.5px] font-semibold">{name}</span>
				<span className="truncate font-mono text-xs text-fg3">{sub}</span>
			</span>
		</div>
	);
}

function CardMeta({ left, right }: { left: string; right: string }) {
	return (
		<div className="flex justify-between font-mono text-xs text-fg3">
			<span>{left}</span>
			<span>{right}</span>
		</div>
	);
}

function NodeCard({
	box,
	node,
	hovered,
	onHover,
	children,
}: {
	box: Box;
	node: NodeKey;
	hovered: NodeKey | null;
	onHover: (node: NodeKey | null) => void;
	children: ReactNode;
}) {
	return (
		<div
			onMouseEnter={() => {
				onHover(node);
			}}
			onMouseLeave={() => {
				onHover(null);
			}}
			className={cn(
				"absolute flex flex-col gap-1.5 rounded-[10px] border bg-background px-3 py-2.5 shadow-xs transition-colors duration-150",
				hovered === node ? "border-primary/50" : "border-line"
			)}
			style={{ left: pctX(box.x), top: pctY(box.y), width: pctX(box.w), height: pctY(box.h) }}
		>
			{children}
		</div>
	);
}

function DiagramLabel({ x, y, className, children }: { x: number; y: number; className?: string | undefined; children: string }) {
	return (
		<div
			className={cn(
				"absolute -translate-x-1/2 -translate-y-1/2 rounded-sm bg-background px-[5px] font-mono text-xs whitespace-nowrap text-fg3",
				className
			)}
			style={{ left: pctX(x), top: pctY(y) }}
		>
			{children}
		</div>
	);
}

function DiagramBackdrop({ hovered }: { hovered: NodeKey | null }) {
	return (
		<svg viewBox={`0 0 ${VIEW_W} ${VIEW_H}`} className="absolute inset-0 size-full" aria-hidden="true">
			<defs>
				<pattern id="splash-grid" width={20} height={20} patternUnits="userSpaceOnUse">
					<circle cx={1} cy={1} r={0.9} className="fill-line" />
				</pattern>
				<marker id="splash-arrow" viewBox="0 0 8 8" refX={7} refY={4} markerWidth={7} markerHeight={7} orient="auto">
					<path d="M0,0 L8,4 L0,8 z" className="fill-line2" />
				</marker>
			</defs>
			<rect x={0} y={0} width={VIEW_W} height={VIEW_H} fill="url(#splash-grid)" />
			<rect
				x={BOX.cluster.x}
				y={BOX.cluster.y}
				width={BOX.cluster.w}
				height={BOX.cluster.h}
				rx={14}
				strokeDasharray="5 5"
				className="fill-primary/[0.03] stroke-primary/30"
			/>
			<rect x={BOX.ns.x} y={BOX.ns.y} width={BOX.ns.w} height={BOX.ns.h} rx={10} className="fill-background stroke-line" />
			{EDGES.map((e, i) => {
				const hot = hovered !== null && e.ends.includes(hovered);
				return (
					<g key={e.id}>
						<path
							d={e.d}
							fill="none"
							strokeWidth={hot ? 1.5 : 1.2}
							markerEnd="url(#splash-arrow)"
							className={hot ? "stroke-primary" : "stroke-line2"}
						/>
						{e.packet && (
							<circle r={2.6} className="fill-primary">
								<animateMotion dur="2.2s" repeatCount="indefinite" path={e.d} begin={`${i * 0.37}s`} />
							</circle>
						)}
					</g>
				);
			})}
		</svg>
	);
}

export function ClusterDiagram({ rollout }: { rollout: Rollout }) {
	const [hovered, setHovered] = useState<NodeKey | null>(null);
	const ready = readyCount(rollout);

	return (
		<div className="relative w-full" style={{ aspectRatio: `${VIEW_W} / ${VIEW_H}` }}>
			<DiagramBackdrop hovered={hovered} />
			<NodeCard box={BOX.client} node="in" hovered={hovered} onHover={setHovered}>
				<CardHead icon={Globe} name="Internet" sub="clients" />
			</NodeCard>
			<NodeCard box={BOX.gateway} node="gw" hovered={hovered} onHover={setHovered}>
				<CardHead icon={ShieldCheck} name="Gateway" sub="envoy" />
				<CardMeta left="HTTP/3" right="TLS 1.3" />
			</NodeCard>
			<NodeCard box={BOX.web} node="web" hovered={hovered} onHover={setHovered}>
				<CardHead icon={AppWindow} name="web" sub="web:sha-5d02aa" />
				<CardMeta left="web.onloco.app" right="2/2" />
				<PodRow pods={WEB_PODS} className="mt-auto" podClassName="size-4" />
			</NodeCard>
			<NodeCard box={BOX.api} node="api" hovered={hovered} onHover={setHovered}>
				<CardHead icon={Server} name="api" sub={`api:sha-${rollout.tag}`} />
				<CardMeta left="api.onloco.app" right={`${ready}/${rollout.target}`} />
				<PodRow pods={rollout.pods} className="mt-auto" podClassName="size-4" />
			</NodeCard>
			<NodeCard box={BOX.worker} node="wk" hovered={hovered} onHover={setHovered}>
				<CardHead icon={Cog} name="worker" sub="worker:sha-e09a77" />
				<CardMeta left="internal" right="1/1" />
				<PodRow pods={WORKER_PODS} className="mt-auto" podClassName="size-4" />
			</NodeCard>
			<NodeCard box={BOX.db} node="db" hovered={hovered} onHover={setHovered}>
				<CardHead icon={Database} name="postgres" sub="postgres:16" />
				<CardMeta left="500m · 1Gi" right="1/1" />
				<PodRow pods={DB_PODS} className="mt-auto" podClassName="size-4" />
			</NodeCard>
			<DiagramLabel x={BOX.cluster.x + 92} y={BOX.cluster.y} className="text-primary">
				us-east-1 · kubernetes
			</DiagramLabel>
			<DiagramLabel x={BOX.ns.x + 90} y={BOX.ns.y}>
				namespace ws-storefront
			</DiagramLabel>
			{EDGES.map((e) => (
				<DiagramLabel key={e.id} x={e.lx} y={e.ly}>
					{e.label}
				</DiagramLabel>
			))}
		</div>
	);
}

function StackedCard({ icon: Icon, name, sub, pods }: { icon: LucideIcon; name: string; sub: string; pods?: readonly PodDot[] | undefined }) {
	return (
		<div className="min-w-0 rounded-[10px] border border-line bg-background px-3 py-2.5">
			<div className="flex items-center gap-2">
				<Icon size={15} strokeWidth={1.6} className="shrink-0 text-foreground" />
				<span className="text-[13.5px] font-semibold">{name}</span>
			</div>
			<div className="mt-0.5 truncate font-mono text-xs text-fg3">{sub}</div>
			{pods !== undefined && <PodRow pods={pods} className="mt-2" podClassName="size-3.5" />}
		</div>
	);
}

function StackedLink({ children }: { children: string }) {
	return (
		<div className="ml-5 flex h-[30px] items-center border-l border-dashed border-line2 pl-[22px] font-mono text-xs whitespace-pre text-fg3">
			{children}
		</div>
	);
}

export function ClusterDiagramStacked({ rollout }: { rollout: Rollout }) {
	return (
		<div className="flex flex-col bg-[radial-gradient(var(--line)_0.9px,transparent_0.9px)] bg-size-[20px_20px] p-4">
			<StackedCard icon={Globe} name="Internet" sub="clients" />
			<StackedLink>https · :443</StackedLink>
			<StackedCard icon={ShieldCheck} name="Gateway" sub="envoy · HTTP/3 · TLS 1.3" />
			<StackedLink>/  ·  /api</StackedLink>
			<div className="grid grid-cols-2 gap-2.5">
				<StackedCard icon={AppWindow} name="web" sub="web:sha-5d02aa" pods={WEB_PODS} />
				<StackedCard icon={Server} name="api" sub={`api:sha-${rollout.tag}`} pods={rollout.pods} />
				<StackedCard icon={Cog} name="worker" sub="worker:sha-e09a77" pods={WORKER_PODS} />
				<StackedCard icon={Database} name="postgres" sub="postgres:16" pods={DB_PODS} />
			</div>
		</div>
	);
}
