import { GitCompareIcon, Undo2Icon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { DeploymentPhase, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { DeploymentPhaseBadge } from "@/components/design/StatusBadge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { pageRangeLabel, pageSlice } from "@/lib/paging";
import { tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { formatStarted } from "./format";
import { depTag, PAGE_SIZES, startedMs, type RegionView } from "./model";

const GRID = "grid grid-cols-[130px_104px_100px_100px_70px_minmax(0,1fr)_110px] items-center gap-3 px-4";

interface Row {
	dep: Deployment;
	region: RegionView;
	prev: Deployment | undefined;
	isCurrent: boolean;
}

function IconAction({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
	return (
		<Tooltip>
			<TooltipTrigger
				render={<Button variant="outline" size="icon-sm" className="text-fg2 hover:text-foreground" aria-label={label} onClick={onClick} />}
			>
				{children}
			</TooltipTrigger>
			<TooltipContent>{label}</TooltipContent>
		</Tooltip>
	);
}

export function DeploymentsSection({
	regions,
	onDiff,
	onRollback,
}: {
	regions: RegionView[];
	onDiff: (region: string, fromId: string, toId: string) => void;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
}) {
	const [page, setPage] = useState(0);
	const [size, setSize] = useState(10);

	const all: Row[] = regions.flatMap((region) =>
		region.history.map((dep, i) => ({
			dep,
			region,
			prev: region.history[i + 1],
			isCurrent: region.current?.id === dep.id,
		})),
	);
	all.sort((a, b) => tsMs(b.dep.createdAt) - tsMs(a.dep.createdAt));
	const slice = pageSlice(all, page, size);

	return (
		<Section
			title={
				<span className="flex items-baseline gap-2.5">
					Deployments
					<span className="text-sm font-normal text-fg3">{all.length} total</span>
				</span>
			}
		>
			<div className="overflow-x-auto">
				<div className="min-w-[860px]">
					<div className={cn(GRID, "h-9 border-b border-line text-sm font-semibold text-fg2")}>
						<span>Started</span>
						<span>Status</span>
						<span>Region</span>
						<span>Image</span>
						<span>Replicas</span>
						<span>Message</span>
						<span />
					</div>
					{slice.rows.length === 0 && <div className="px-4 py-5 text-fg3">No deployments yet.</div>}
					{slice.rows.map(({ dep, region, prev, isCurrent }) => {
						const canRollback = !isCurrent && dep.status !== DeploymentPhase.FAILED && dep.spec !== undefined;
						return (
							<div
								key={dep.id}
								className={cn(GRID, "group h-[42px] border-b border-line hover:bg-bg2", isCurrent && "bg-bg2")}
							>
								<span className="text-fg2 tabular-nums">{formatStarted(startedMs(dep))}</span>
								<DeploymentPhaseBadge phase={dep.status} />
								<span className="truncate text-fg2">{region.name}</span>
								<span className="truncate" title={depTag(dep)}>
									{depTag(dep)}
								</span>
								<span className="text-fg2 tabular-nums">{dep.replicas}</span>
								<span className="truncate text-fg2" title={dep.message}>
									{dep.message}
								</span>
								<div className="flex items-center justify-end gap-1">
									{isCurrent && <span className="mr-1 text-sm text-fg3">current</span>}
									<div className="flex gap-1 opacity-0 transition-opacity duration-120 group-hover:opacity-100 focus-within:opacity-100">
										{prev !== undefined && (
											<IconAction
												label="Compare with previous deployment"
												onClick={() => {
													onDiff(region.name, prev.id, dep.id);
												}}
											>
												<GitCompareIcon />
											</IconAction>
										)}
										{canRollback && (
											<IconAction
												label="Roll back to this deployment"
												onClick={() => {
													onRollback(dep, region.current);
												}}
											>
												<Undo2Icon />
											</IconAction>
										)}
									</div>
								</div>
							</div>
						);
					})}
				</div>
			</div>
			<Pager
				label={all.length > 0 ? pageRangeLabel(slice.page, size, all.length) : "0 of 0"}
				pageSizes={PAGE_SIZES}
				pageSize={size}
				onPageSize={(n) => {
					setSize(n);
					setPage(0);
				}}
				onPrev={slice.page > 0 ? () => { setPage(slice.page - 1); } : null}
				onNext={slice.page < slice.pages - 1 ? () => { setPage(slice.page + 1); } : null}
			/>
		</Section>
	);
}
