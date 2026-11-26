import { ServerIcon } from "lucide-react";

import { ErrorCard } from "@/components/ErrorCard";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, PageLoading, Section } from "@/components/design/Page";
import { pluralize } from "@/lib/format";
import { regionGroups } from "@/lib/system";

import { Capacity } from "./Capacity";
import { availableCapacity, runsApps } from "./capacity-model";
import { ClusterTable } from "./ClusterTable";
import { useFleet } from "./shared";

export function SystemOverview() {
	const fleet = useFleet();
	if (fleet.isLoading) return <PageLoading />;
	if (fleet.error !== null) {
		return <ErrorCard error={fleet.error} fallbackMessage="Failed to load the clusters" minHeight="min-h-48" />;
	}
	const groups = regionGroups(fleet.clusters.filter((c) => c.removedAt === undefined));
	const headroom = availableCapacity(fleet.clusters);
	const appClusters = fleet.clusters.filter(runsApps);
	const activeClusters = appClusters.filter((cluster) => cluster.isActive && cluster.removedAt === undefined).length;
	return (
		<Page>
			<PageHeader title="Overview" />
			{groups.length === 0 ? (
				<Section>
					<EmptyState icon={<ServerIcon />} title="No clusters" />
				</Section>
			) : (
				<>
					<Section title="Capacity" meta={`${headroom.included}/${activeClusters} app clusters`}>
						<Capacity capacity={headroom.capacity} reportLabel="Oldest report" />
					</Section>
					{groups.map((group) => (
						<Section
							key={group.region}
							title={<span className="font-mono">{group.region}</span>}
							count={pluralize(group.clusters.length, "cluster")}
						>
							<ClusterTable
								rows={group.clusters}
								apiVersion={fleet.apiVersion}
								canChange={false}
								label={`Clusters in ${group.region}`}
							/>
						</Section>
					))}
				</>
			)}
		</Page>
	);
}
