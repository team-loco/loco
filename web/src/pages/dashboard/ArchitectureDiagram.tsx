import { useQuery } from "@connectrpc/connect-query";
import { GlobeIcon, ShieldCheckIcon } from "lucide-react";
import { useCallback, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";

import { Button } from "@/components/design/Button";
import { resourceStatusStyle } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { pluralize } from "@/lib/format";
import { servedDomains } from "@/lib/new-resource";
import { primaryDomain } from "@/lib/resource-status";
import { cn } from "@/lib/utils";

import {
	MAP_LABEL_H,
	MAP_MORE_H,
	MAP_NODE_H,
	MAP_NODE_W,
	mapLayout,
	type MapGap,
	type MapNode,
	type MapPoint,
} from "./mapLayout";
import type { EnvResource } from "./useDashboardData";

const NODE =
	"absolute flex flex-col justify-center gap-0.5 rounded-lg px-3 py-[9px] text-foreground no-underline hover:no-underline";
const RESOURCE_NODE = cn(
	NODE,
	"bg-raise ring-1 ring-line2 hover:ring-fg3 focus-visible:ring-2 focus-visible:ring-ring",
);
const EDGE_NODE = cn(NODE, "bg-panel2");

function curve(width: number, y1: number, y2: number): string {
	const w = width.toString();
	const mx = (width / 2).toString();
	return `M0,${y1.toString()} C${mx},${y1.toString()} ${mx},${y2.toString()} ${w},${y2.toString()}`;
}

function box(p: MapPoint) {
	return { left: p.x, top: p.y, width: MAP_NODE_W, height: MAP_NODE_H };
}

function GapEdges({ gap, height }: { gap: MapGap; height: number }) {
	return (
		<svg
			width={gap.width}
			height={height}
			aria-hidden="true"
			className="pointer-events-none absolute top-0 fill-none stroke-fg4 stroke-[1.5]"
			style={{ left: gap.x }}
		>
			{gap.edges.map((e) => (
				<path key={e.key} d={curve(gap.width, e.y1, e.y2)} />
			))}
			{gap.divider && (
				<line
					x1={gap.width / 2}
					y1={0}
					x2={gap.width / 2}
					y2={height}
					className="stroke-line2 [stroke-dasharray:4_4]"
				/>
			)}
		</svg>
	);
}

function NodeText({ icon, title, sub, meta }: { icon: ReactNode; title: string; sub: string; meta: string }) {
	return (
		<>
			<span className="flex min-w-0 items-center gap-2 text-[13px] font-medium">
				{icon}
				<span className="truncate font-mono">{title}</span>
			</span>
			<span className="truncate font-mono text-[11.5px] text-fg3">{sub}</span>
			{meta !== "" && <span className="truncate text-[11.5px] text-fg3">{meta}</span>}
		</>
	);
}

function ResourceNode({ node, sub, href }: { node: MapNode; sub: string; href: string }) {
	const { row } = node;
	return (
		<Link to={href} className={RESOURCE_NODE} style={box(node)}>
			<NodeText
				icon={<StatusGlyph glyph={resourceStatusStyle(row.status).glyph} />}
				title={row.resource.name}
				sub={sub}
				meta={row.regions.map((g) => g.region).join(" · ")}
			/>
		</Link>
	);
}

function useWidth(): [(el: HTMLDivElement | null) => (() => void) | undefined, number] {
	const [width, setWidth] = useState(0);
	const measure = useCallback((el: HTMLDivElement | null) => {
		if (el === null) return undefined;
		const observer = new ResizeObserver(([entry]) => {
			if (entry !== undefined) setWidth(entry.contentRect.width);
		});
		observer.observe(el);
		return () => {
			observer.disconnect();
		};
	}, []);
	return [measure, width];
}

export function ArchitectureDiagram({
	rows,
	regionOrder,
	environmentId,
	hrefFor,
	onShowTable,
}: {
	rows: EnvResource[];
	regionOrder: string[];
	environmentId: string;
	hrefFor: (id: string) => string;
	onShowTable: () => void;
}) {
	const { data: domainsRes } = useQuery(listPlatformDomains, { environmentId });
	const platformDomains = domainsRes?.platformDomains ?? [];
	const [measure, available] = useWidth();
	const layout = mapLayout(rows, regionOrder, available);
	const more = (n: number, p: MapPoint) => (
		<div
			className="absolute flex items-center justify-center"
			style={{ left: p.x, top: p.y, width: MAP_NODE_W, height: MAP_MORE_H }}
		>
			<Button variant="link" onClick={onShowTable}>
				{`${n.toString()} more in the table`}
			</Button>
		</div>
	);

	return (
		<div className="overflow-x-auto px-5 pt-3 pb-5">
			<div ref={measure} className="min-w-0">
				{available > 0 && (
					<div className="relative" style={{ width: layout.width, height: layout.height }}>
						{layout.gaps.map((gap) => (
							<GapEdges key={gap.key} gap={gap} height={layout.height} />
						))}
						{layout.internet !== null && (
							<div className={EDGE_NODE} style={box(layout.internet)}>
								<NodeText
									icon={<GlobeIcon className="size-[15px] shrink-0 text-fg3" />}
									title="Internet"
									sub="HTTPS · HTTP/3"
									meta=""
								/>
							</div>
						)}
						{layout.gateways.map((g) => {
							const domain = servedDomains(platformDomains, [g.region])[0] ?? "";
							return (
								<div key={g.region} className={EDGE_NODE} style={box(g)}>
									<NodeText
										icon={<ShieldCheckIcon className="size-[15px] shrink-0 text-fg3" />}
										title={g.region}
										sub={domain === "" ? "Gateway" : `*.${domain}`}
										meta={`${pluralize(g.resources, "route")} · ${pluralize(g.replicas, "replica")}`}
									/>
								</div>
							);
						})}
						{layout.publicNodes.map((node) => (
							<ResourceNode
								key={node.row.resource.id}
								node={node}
								sub={primaryDomain(node.row.resource) ?? ""}
								href={hrefFor(node.row.resource.id)}
							/>
						))}
						{layout.publicMore !== null && more(layout.hiddenPublic, layout.publicMore)}
						{layout.internalLabel !== null && (
							<div
								className="absolute flex items-center text-[12px] text-fg3"
								style={{
									left: layout.internalLabel.x,
									top: layout.internalLabel.y,
									width: MAP_NODE_W,
									height: MAP_LABEL_H,
								}}
							>
								Internal · no public route
							</div>
						)}
						{layout.internalNodes.map((node) => (
							<ResourceNode
								key={node.row.resource.id}
								node={node}
								sub="internal"
								href={hrefFor(node.row.resource.id)}
							/>
						))}
						{layout.internalMore !== null && more(layout.hiddenInternal, layout.internalMore)}
					</div>
				)}
			</div>
		</div>
	);
}
