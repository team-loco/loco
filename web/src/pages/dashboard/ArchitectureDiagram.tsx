import { BoxIcon, GlobeIcon, RouterIcon, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "@/components/design/Badge";
import { Section } from "@/components/design/Page";
import { resourceStatusStyle } from "@/components/design/StatusBadge";
import { pluralize } from "@/lib/format";
import { cn } from "@/lib/utils";


export interface DiagramService {
	key: string;
	name: string;
	status: ResourceStatus | "draft";
	domain: string | null;
	regions: { region: string; replicas: number }[];
	href?: string | undefined;
	onOpen?: (() => void) | undefined;
}

const SX = 20;
const GX = 220;
const RX = 460;
const W0 = 140;
const W1 = 180;
const W2 = 300;
const H = 60;
const GAP = 14;
const PAD = 24;

interface Edge {
	x1: number;
	y1: number;
	x2: number;
	y2: number;
	dashed: boolean;
}

function edgePath(e: Edge): string {
	const mx = (e.x1 + e.x2) / 2;
	return `M${e.x1.toString()},${e.y1.toString()} C${mx.toString()},${e.y1.toString()} ${mx.toString()},${e.y2.toString()} ${e.x2.toString()},${e.y2.toString()}`;
}

function isWarn(status: ResourceStatus | "draft"): boolean {
	return status === ResourceStatus.DEGRADED || status === ResourceStatus.UNAVAILABLE;
}

function NodeBody({
	icon: Icon,
	title,
	badge,
	sub,
}: {
	icon: LucideIcon;
	title: string;
	badge?: ReactNode;
	sub: string;
}) {
	return (
		<>
			<span className="flex min-w-0 items-center gap-2">
				<Icon className="size-[15px] shrink-0 text-fg3" />
				<span className="flex-1 truncate font-semibold">{title}</span>
				{badge}
			</span>
			<span className="truncate text-sm text-fg3">{sub}</span>
		</>
	);
}

const nodeClass =
	"absolute flex flex-col justify-center gap-1 rounded-lg border px-3 leading-[1.3] text-foreground no-underline hover:no-underline";

export function ArchitectureDiagram({
	services,
	regionOrder,
}: {
	services: DiagramService[];
	regionOrder: string[];
}) {
	const legend = (
		<div className="flex gap-4 text-sm text-fg3">
			<span className="flex items-center gap-1.5">
				<span className="w-5 border-t-2 border-fg4" />
				public route
			</span>
			<span className="flex items-center gap-1.5">
				<span className="w-5 border-t-2 border-dashed border-line2" />
				internal only
			</span>
		</div>
	);

	const sorted = services.toSorted(
		(a, b) =>
			Number(a.status === "draft") - Number(b.status === "draft") ||
			Number(a.domain === null) - Number(b.domain === null),
	);
	const sy = (i: number) => PAD + i * (H + GAP);
	const svcY = new Map<string, number>();
	sorted.forEach((s, i) => svcY.set(s.key, sy(i)));
	const total = sorted.length === 0 ? 0 : sy(sorted.length - 1) + H + PAD;

	const regionNames = new Set(regionOrder);
	for (const s of sorted) {
		for (const g of s.regions) regionNames.add(g.region);
	}

	const edges: Edge[] = [];
	const gateways: { region: string; y: number; services: number; replicas: number }[] = [];
	for (const region of regionNames) {
		const mine = sorted.filter((s) => s.regions.some((g) => g.region === region));
		if (mine.length === 0) continue;
		const ys = mine.map((s) => (svcY.get(s.key) ?? 0) + H / 2);
		const cy = ys.reduce((a, b) => a + b, 0) / ys.length;
		const gy = Math.min(Math.max(cy - H / 2, PAD), total - PAD - H);
		const replicas = mine.reduce(
			(n, s) => n + s.regions.filter((g) => g.region === region).reduce((m, g) => m + g.replicas, 0),
			0,
		);
		gateways.push({ region, y: gy, services: mine.length, replicas });
		for (const s of mine) {
			edges.push({ x1: GX + W1, y1: gy + H / 2, x2: RX, y2: (svcY.get(s.key) ?? 0) + H / 2, dashed: s.domain === null });
		}
	}
	const iy = gateways.length > 0 ? gateways.reduce((a, g) => a + g.y, 0) / gateways.length : PAD;
	for (const g of gateways) edges.push({ x1: SX + W0, y1: iy + H / 2, x2: GX, y2: g.y + H / 2, dashed: false });

	return (
		<Section title="Architecture" actions={legend}>
			{sorted.length === 0 ? (
				<div className="flex flex-col items-center justify-center gap-3 rounded-b-xl bg-[radial-gradient(var(--line)_1px,transparent_1px)] bg-size-[16px_16px] px-4 py-14 text-center">
					<span className="font-semibold">Nothing deployed here yet</span>
					<span className="max-w-sm text-fg3">Services you deploy to this environment show up here with their routes.</span>
				</div>
			) : (
				<div className="overflow-x-auto rounded-b-xl bg-[radial-gradient(var(--line)_1px,transparent_1px)] bg-size-[16px_16px]">
					<div className="relative" style={{ height: total, width: RX + W2 + PAD }}>
						<svg width={RX + W2 + PAD} height={total} className="pointer-events-none absolute top-0 left-0">
							{edges.map((e, i) => (
								<path
									key={i}
									d={edgePath(e)}
									fill="none"
									strokeWidth={1.5}
									strokeDasharray={e.dashed ? "4 4" : undefined}
									className={e.dashed ? "stroke-line2" : "stroke-fg4"}
								/>
							))}
						</svg>
						<div className={cn(nodeClass, "border-line bg-bg2")} style={{ left: SX, top: iy, width: W0, height: H }}>
							<NodeBody icon={GlobeIcon} title="Internet" sub="HTTPS · HTTP/3" />
						</div>
						{gateways.map((g) => (
							<div
								key={g.region}
								className={cn(nodeClass, "border-line bg-bg2")}
								style={{ left: GX, top: g.y, width: W1, height: H }}
							>
								<NodeBody
									icon={RouterIcon}
									title={g.region}
									sub={`${pluralize(g.services, "service")} · ${pluralize(g.replicas, "replica")} · Envoy`}
								/>
							</div>
						))}
						{sorted.map((s) => {
							const isDraft = s.status === "draft";
							const style = s.status === "draft" ? null : resourceStatusStyle(s.status);
							const badge = (
								<Badge size="sm" tone={style?.tone ?? "muted"}>
									{style?.label ?? "Draft"}
								</Badge>
							);
							const regionsText = s.regions.map((g) => g.region).join(", ");
							const sub = isDraft
								? `Not deployed · ${regionsText}`
								: (s.domain ?? `internal · ${regionsText}`);
							const cls = cn(
								nodeClass,
								"hover:border-foreground",
								isDraft
									? "border-dashed border-line2 bg-bg2 opacity-75"
									: isWarn(s.status)
										? "border-warn-fg bg-background"
										: "border-line bg-background",
							);
							const pos = { left: RX, top: svcY.get(s.key) ?? 0, width: W2, height: H };
							const body = <NodeBody icon={BoxIcon} title={s.name} badge={badge} sub={sub} />;
							if (s.onOpen !== undefined) {
								return (
									<button key={s.key} type="button" className={cn(cls, "text-left")} style={pos} onClick={s.onOpen}>
										{body}
									</button>
								);
							}
							return (
								<Link key={s.key} to={s.href ?? "#"} className={cls} style={pos}>
									{body}
								</Link>
							);
						})}
					</div>
				</div>
			)}
		</Section>
	);
}
