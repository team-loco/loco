import { useQuery } from "@connectrpc/connect-query";
import { GlobeIcon, ShieldCheckIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";

import { Button } from "@/components/design/Button";
import { resourceStatusStyle } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { pluralize } from "@/lib/format";
import { servedDomains } from "@/lib/new-resource";
import { primaryDomain } from "@/lib/resource-status";
import { cn } from "@/lib/utils";

import { mapLayout } from "./resourceRows";
import type { EnvResource } from "./useDashboardData";

const GATEWAY_X = 196;
const PUBLIC_X = 404;
const EDGE_W = 160;
const PUBLIC_W = 200;
const CANVAS_W = PUBLIC_X + PUBLIC_W;

const NODE =
	"absolute flex flex-col justify-center gap-0.5 rounded-lg px-3 py-[9px] text-foreground no-underline hover:no-underline";
const RESOURCE_NODE = cn(
	NODE,
	"bg-raise ring-1 ring-line2 hover:ring-fg3 focus-visible:ring-2 focus-visible:ring-ring",
);
const EDGE_NODE = cn(NODE, "bg-panel2");

function curve(x1: number, y1: number, x2: number, y2: number): string {
	const mx = (x1 + x2) / 2;
	return `M${x1.toString()},${y1.toString()} C${mx.toString()},${y1.toString()} ${mx.toString()},${y2.toString()} ${x2.toString()},${y2.toString()}`;
}

function rowGlyph(row: EnvResource) {
	return <StatusGlyph glyph={resourceStatusStyle(row.status).glyph} />;
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
	const layout = mapLayout(rows, regionOrder);
	const h = layout.nodeHeight;
	const mid = (y: number) => y + h / 2;
	const hasPublic = layout.publicNodes.length > 0;
	const moreButton = (n: number, what: string) => (
		<Button variant="link" className="self-start" onClick={onShowTable}>
			{`${n.toString()} more ${what} in the table`}
		</Button>
	);

	return (
		<div className="overflow-x-auto px-5 pt-2 pb-5">
			<div className="flex w-max items-start gap-6">
				{hasPublic && (
					<div className="relative shrink-0" style={{ width: CANVAS_W, height: layout.height }}>
						<svg
							width={CANVAS_W}
							height={layout.height}
							aria-hidden="true"
							className="pointer-events-none absolute inset-0 fill-none stroke-fg4 stroke-[1.5]"
						>
							{layout.gateways.map((g) => (
								<path key={g.region} d={curve(EDGE_W, mid(layout.internetY), GATEWAY_X, mid(g.y))} />
							))}
							{layout.edges.map((e) => (
								<path key={`${e.from}/${e.to}`} d={curve(GATEWAY_X + EDGE_W, mid(e.y1), PUBLIC_X, mid(e.y2))} />
							))}
						</svg>
						<div className={EDGE_NODE} style={{ left: 0, top: layout.internetY, width: EDGE_W, height: h }}>
							<NodeText
								icon={<GlobeIcon className="size-[15px] shrink-0 text-fg3" />}
								title="Internet"
								sub="HTTPS · HTTP/3"
								meta=""
							/>
						</div>
						{layout.gateways.map((g) => {
							const domain = servedDomains(platformDomains, [g.region])[0] ?? "";
							return (
								<div
									key={g.region}
									className={EDGE_NODE}
									style={{ left: GATEWAY_X, top: g.y, width: EDGE_W, height: h }}
								>
									<NodeText
										icon={<ShieldCheckIcon className="size-[15px] shrink-0 text-fg3" />}
										title={g.region}
										sub={domain === "" ? "Gateway" : `*.${domain}`}
										meta={`${pluralize(g.resources, "route")} · ${pluralize(g.replicas, "replica")}`}
									/>
								</div>
							);
						})}
						{layout.publicNodes.map(({ row, y }) => (
							<Link
								key={row.resource.id}
								to={hrefFor(row.resource.id)}
								className={RESOURCE_NODE}
								style={{ left: PUBLIC_X, top: y, width: PUBLIC_W, height: h }}
							>
								<NodeText
									icon={rowGlyph(row)}
									title={row.resource.name}
									sub={primaryDomain(row.resource) ?? ""}
									meta={row.regions.map((g) => g.region).join(" · ")}
								/>
							</Link>
						))}
						{layout.hiddenPublic > 0 && (
							<div className="absolute flex items-center" style={{ left: PUBLIC_X, top: layout.moreY, height: h }}>
								{moreButton(layout.hiddenPublic, "public")}
							</div>
						)}
					</div>
				)}
				{layout.internal.length > 0 && (
					<div className="flex w-[220px] shrink-0 flex-col gap-1 rounded-lg bg-panel2 p-2">
						<span className="px-2 pt-1 pb-1.5 text-[12.5px] text-fg3">Internal only · no public route</span>
						{layout.internal.map((row) => (
							<Link
								key={row.resource.id}
								to={hrefFor(row.resource.id)}
								className="flex flex-col gap-0.5 rounded-md bg-raise px-2.5 py-2 text-foreground no-underline ring-1 ring-line2 hover:no-underline hover:ring-fg3"
							>
								<NodeText
									icon={rowGlyph(row)}
									title={row.resource.name}
									sub={row.regions.map((g) => g.region).join(" · ")}
									meta=""
								/>
							</Link>
						))}
						{layout.hiddenInternal > 0 && (
							<div className="px-2 py-1">{moreButton(layout.hiddenInternal, "internal")}</div>
						)}
					</div>
				)}
			</div>
		</div>
	);
}
