import { PlusIcon, SearchXIcon, ServerIcon } from "lucide-react";
import { parseAsBoolean, parseAsString, useQueryStates } from "nuqs";
import { Link } from "react-router";

import { ErrorCard } from "@/components/ErrorCard";
import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { PickFilter } from "@/components/design/FilterPopover";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleChip } from "@/components/design/ToggleChip";
import { NEW_CLUSTER_PATH, systemAccess } from "@/lib/system";
import { useMyScopes } from "@/hooks/useMyScopes";

import { ClusterTable } from "./ClusterTable";
import { useFleet } from "./shared";

const PARAMS = {
	q: parseAsString.withDefault(""),
	region: parseAsString.withDefault(""),
	removed: parseAsBoolean.withDefault(false),
};

export function SystemClusters() {
	const fleet = useFleet();
	const canChange = systemAccess(useMyScopes().scopes) === "admin";
	const [{ q, region, removed }, setParams] = useQueryStates(PARAMS, { history: "replace" });
	const needle = q.trim().toLowerCase();
	const rows = fleet.clusters
		.filter((c) => (c.removedAt !== undefined) === removed)
		.filter((c) => c.name.toLowerCase().includes(needle) && (region === "" || c.region === region))
		.map((cluster) => ({ cluster, state: fleet.stateOf(cluster) }))
		.toSorted(
			(a, b) => a.cluster.region.localeCompare(b.cluster.region) || a.cluster.name.localeCompare(b.cluster.name),
		);
	const filtered = needle !== "" || region !== "";
	const empty = filtered ? (
		<EmptyState icon={<SearchXIcon />} title="No clusters match" query={needle}>
			Clear the name or region filter to see the others.
		</EmptyState>
	) : (
		<EmptyState icon={<ServerIcon />} title={removed ? "No removed clusters" : "No clusters yet"}>
			{removed
				? "Clusters you remove appear here until you restore them."
				: "Add a cluster to give Loco somewhere to run workloads."}
		</EmptyState>
	);

	return (
		<Page>
			<PageHeader
				title="Clusters"
				actions={
					canChange && (
						<Button nativeButton={false} render={<Link to={NEW_CLUSTER_PATH} />}>
							<PlusIcon />
							Add cluster
						</Button>
					)
				}
			/>
			{fleet.error !== null ? (
				<ErrorCard error={fleet.error} fallbackMessage="Failed to load the clusters" minHeight="min-h-48" />
			) : (
				<Section
					filters={
						<>
							<PickFilter
								icon={<ServerIcon />}
								label="Region"
								items={fleet.regions.map((r) => ({ id: r.code, name: r.code }))}
								counts={new Map(fleet.regions.map((r) => [r.code, r.clusterCount]))}
								value={region}
								onChange={(code) => {
									void setParams({ region: code === "" ? null : code });
								}}
							/>
							<ToggleChip
								variant="button"
								pressed={removed}
								onPressedChange={(on) => {
									void setParams({ removed: on ? true : null });
								}}
							>
								Removed
							</ToggleChip>
						</>
					}
					search={
						<SearchInput
							value={q}
							onChange={(e) => {
								void setParams({ q: e.target.value === "" ? null : e.target.value });
							}}
							onClear={() => {
								void setParams({ q: null });
							}}
							placeholder="Filter by name"
							aria-label="Filter clusters by name"
						/>
					}
				>
					<ClusterTable
						rows={rows}
						apiVersion={fleet.apiVersion}
						canChange={canChange}
						byRegion
						loading={fleet.isLoading}
						empty={empty}
						label="Clusters"
					/>
				</Section>
			)}
		</Page>
	);
}
