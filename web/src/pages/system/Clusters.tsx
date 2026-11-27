import type { Cluster } from "@gen/loco/cluster/v1/cluster_pb";
import { PlusIcon, SearchXIcon, ServerIcon } from "lucide-react";
import { parseAsBoolean, parseAsString, useQueryStates } from "nuqs";
import { Link } from "react-router";

import { PartialError } from "@/components/design/ErrorState";
import { getErrorMessage } from "@/lib/error-handler";
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
import { useClusterCapacities, useFleet } from "./shared";

const PARAMS = {
	q: parseAsString.withDefault(""),
	region: parseAsString.withDefault(""),
	removed: parseAsBoolean.withDefault(false),
};

export function SystemClusters() {
	const fleet = useFleet();
	const capacities = useClusterCapacities();
	const canChange = systemAccess(useMyScopes().scopes) === "admin";
	const [{ q, region, removed }, setParams] = useQueryStates(PARAMS, { history: "replace" });
	const needle = q.trim().toLowerCase();
	const matchesName = (c: Cluster) => c.name.toLowerCase().includes(needle);
	const matchesRegion = (c: Cluster) => region === "" || c.region === region;
	const rows = fleet.clusters
		.filter((c) => (c.removedAt !== undefined) === removed)
		.filter((c) => matchesName(c) && matchesRegion(c))
		.map((cluster) => ({ cluster, state: fleet.stateOf(cluster) }))
		.toSorted(
			(a, b) => a.cluster.region.localeCompare(b.cluster.region) || a.cluster.name.localeCompare(b.cluster.name),
		);
	const filtered = needle !== "" || region !== "";
	const empty = filtered ? (
		<EmptyState icon={<SearchXIcon />} title="No matches" query={needle} />
	) : (
		<EmptyState icon={<ServerIcon />} title={removed ? "No removed clusters" : "No clusters"} />
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
								counts={new Map(fleet.regions.map((r) => [r.code, r.count]))}
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
					{capacities.query.error !== null && (
						<PartialError
							title="Capacity unavailable"
							detail={getErrorMessage(capacities.query.error, "Failed to load cluster capacity")}
							error={capacities.query.error}
							onRetry={async () => await capacities.query.refetch()}
						/>
					)}
					<ClusterTable
						capacities={capacities.byCluster}
						capacityLoading={capacities.query.isLoading}
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
