import { useQuery } from "@connectrpc/connect-query";
import { ArrowRightIcon, ScrollTextIcon, WaypointsIcon } from "lucide-react";
import { useQueryStates } from "nuqs";
import { useState } from "react";
import { Link } from "react-router";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { Button } from "@/components/design/Button";
import { Section } from "@/components/design/Page";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ResourceStatusBadge, RolloutBadge } from "@/components/design/StatusBadge";
import { deploymentImage } from "@/lib/deployment-utils";
import { imageVersion } from "@/lib/image";
import { primaryDomain, rolloutState } from "@/lib/resource-status";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { ArchitectureDiagram } from "@/pages/dashboard/ArchitectureDiagram";
import { isDraftItem, type DashboardItem } from "@/pages/dashboard/resourceRows";
import { useDashboardData, type EnvResource } from "@/pages/dashboard/useDashboardData";

import { statusCounts, statusInput, useMatchingResourceIds } from "./query";
import { ResourceSearch, StatusFilter } from "./ResourceFilters";
import { ALL, DRAFT, FILTER_PARSERS, FILTER_URL_KEYS } from "./table";

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<div className="flex flex-col gap-1">
			<span className="text-[12px] text-fg3">{label}</span>
			<span className="font-mono text-[13px] break-all">{children}</span>
		</div>
	);
}

export function ResourcesMap({
	orgId,
	workspaceId,
	env,
	onShowTable,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	onShowTable: () => void;
}) {
	const data = useDashboardData(orgId, workspaceId, env.id);
	const [params, setParams] = useQueryStates(FILTER_PARSERS, { history: "replace", urlKeys: FILTER_URL_KEYS });
	// The map has no region filter and reads every environment's status; it dims what the filters leave out.
	const filter = { query: params.query, status: params.status };
	const filtering = filter.query.trim() !== "" || filter.status !== ALL;
	const counted = { workspaceId, query: filter.query.trim() };
	const counts = useQuery(listWorkspaceResources, {
		...counted,
		pageSize: 1,
		includeTotalCount: true,
		includeFacets: true,
	}).data;
	const matched = useMatchingResourceIds({ ...counted, ...statusInput(filter.status) }, filtering).data;
	// The map draws no drafts, so the Draft filter leaves nothing lit.
	const matching = filter.status === DRAFT ? new Set<string>() : matched;
	// Kept after close so the panel keeps its contents while it slides out.
	const [selectedId, setSelectedId] = useState<string | null>(null);
	const [open, setOpen] = useState(false);
	const deployed = data.envResources.filter((r) => r.last !== undefined);
	const selected = deployed.find((r) => r.resource.id === selectedId);
	const envParam = { env: env.slug };
	const version = selected?.last === undefined ? "—" : imageVersion(deploymentImage(selected.last));
	const hrefFor = (item: DashboardItem) =>
		isDraftItem(item) ? "" : resourcePath(orgId, workspaceId, item.resource.id, { env: env.slug });

	return (
		<Section
			filters={
				<StatusFilter
					counts={statusCounts(counts?.totalCount, counts?.facets, 0)}
					status={filter.status}
					onStatus={(status) => {
						void setParams({ status });
					}}
				/>
			}
			search={
				<ResourceSearch
					query={filter.query}
					onQuery={(query) => {
						void setParams({ query });
					}}
				/>
			}
		>
			{deployed.length === 0 ? (
				<div className="py-10 text-center text-fg3">Nothing is deployed to {env.name} yet.</div>
			) : (
				<ArchitectureDiagram
					rows={deployed}
					regionOrder={data.regions.map((r) => r.region)}
					environmentId={env.id}
					hrefFor={hrefFor}
					onShowTable={onShowTable}
					dimmed={(item) => filtering && (isDraftItem(item) || matching?.has(item.resource.id) === false)}
					onSelect={(item: EnvResource) => {
						setSelectedId(item.resource.id);
						setOpen(true);
					}}
				/>
			)}
			<Sheet open={open} onOpenChange={setOpen}>
				{selected !== undefined && (
					<SheetContent>
						<SheetHeader>
							<SheetTitle className="truncate">{selected.resource.name}</SheetTitle>
							<div className="flex flex-wrap items-center gap-1.5">
								<ResourceStatusBadge status={selected.status} />
								<RolloutBadge rollout={rolloutState(selected.environment?.regions ?? [])} />
							</div>
						</SheetHeader>
						<SheetBody>
							{(selected.resource.description ?? "") !== "" && (
								<p className="m-0 text-[13.5px] text-fg2">{selected.resource.description}</p>
							)}
							<Fact label="Version">{version}</Fact>
							<Fact label="Replicas">
								{selected.regions.map((g) => `${g.region} ${g.ready.toString()}/${g.desired.toString()}`).join(" · ")}
							</Fact>
							<Fact label="Address">{primaryDomain(selected.resource) ?? "internal only"}</Fact>
						</SheetBody>
						<SheetFooter className="justify-start">
							<Button
								variant="outline"
								nativeButton={false}
								render={
									<Link
										to={observabilityPath(orgId, workspaceId, "logs", { ...envParam, r: [selected.resource.slug] })}
									/>
								}
							>
								<ScrollTextIcon />
								Logs
							</Button>
							<Button
								variant="outline"
								nativeButton={false}
								render={
									<Link
										to={observabilityPath(orgId, workspaceId, "traces", { ...envParam, r: [selected.resource.slug] })}
									/>
								}
							>
								<WaypointsIcon />
								Traces
							</Button>
							<Button nativeButton={false} className="ml-auto" render={<Link to={hrefFor(selected)} />}>
								Open {selected.resource.name}
								<ArrowRightIcon />
							</Button>
						</SheetFooter>
					</SheetContent>
				)}
			</Sheet>
		</Section>
	);
}
