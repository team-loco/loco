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

import { MAP_LABEL_H, MAP_MORE_H, MAP_NODE_H, MAP_NODE_W, mapLayout, type MapNode, type MapPoint } from "./mapLayout";
import type { EnvResource } from "./useDashboardData";

const NODE =
	"absolute flex flex-col justify-center gap-0.5 rounded-lg px-3 py-[9px] text-foreground no-underline hover:no-underline";
const RESOURCE_NODE = cn(
	NODE,
	"bg-raise ring-1 ring-line2 hover:ring-fg3 focus-visible:ring-2 focus-visible:ring-ring",
);
const EDGE_NODE = cn(NODE, "bg-panel2");

function curve(x1: number, y1: number, x2: number, y2: number): string {
	const my = (y1 + y2) / 2;
	return `M${x1.toString()},${y1.toString()} C${x1.toString()},${my.toString()} ${x2.toString()},${my.toString()} ${x2.toString()},${y2.toString()}`;
}

function box(p: MapPoint) {
	return { left: p.x, top: p.y, width: MAP_NODE_W, height: MAP_NODE_H };
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
	const more = (n: number, what: string, y: number) => (
		<div className="absolute inset-x-0 flex items-center justify-center" style={{ top: y, height: MAP_MORE_H }}>
			<Button variant="link" onClick={onShowTable}>
				{`${n.toString()} more ${what} in the table`}
			</Button>
		</div>
	);

	return (
		<div className="overflow-x-auto px-5 pt-3 pb-5">
			<div ref={measure} className="min-w-0">
				{available > 0 && (
					<div className="relative" style={{ width: layout.width, height: layout.height }}>
						<svg
							width={layout.width}
							height={layout.height}
							aria-hidden="true"
							className="pointer-events-none absolute inset-0 fill-none stroke-fg4 stroke-[1.5]"
						>
							{layout.edges.map((e) => (
								<path key={e.key} d={curve(e.x1, e.y1, e.x2, e.y2)} />
							))}
						</svg>
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
						{layout.hiddenPublic > 0 && more(layout.hiddenPublic, "public", layout.publicMoreY)}
						{layout.internalNodes.length > 0 && (
							<div
								className="absolute inset-x-0 flex items-center gap-3 text-[12px] text-fg3"
								style={{ top: layout.internalLabelY, height: MAP_LABEL_H }}
							>
								<span className="flex-1 border-t border-dashed border-line2" />
								Internal only · no public route
								<span className="flex-1 border-t border-dashed border-line2" />
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
						{layout.hiddenInternal > 0 && more(layout.hiddenInternal, "internal", layout.internalMoreY)}
					</div>
				)}
			</div>
		</div>
	);
}
