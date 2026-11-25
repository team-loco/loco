import { ServerIcon } from "lucide-react";

import { ErrorCard } from "@/components/ErrorCard";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, PageLoading, Section } from "@/components/design/Page";
import { pluralize } from "@/lib/format";
import { regionGroups, stateRank } from "@/lib/system";

import { ClusterTable } from "./ClusterTable";
import { useFleet } from "./shared";

const HEALTHY_RANK = stateRank("healthy");

export function SystemOverview() {
	const fleet = useFleet();
	if (fleet.isLoading) return <PageLoading />;
	if (fleet.error !== null) {
		return <ErrorCard error={fleet.error} fallbackMessage="Failed to load the clusters" minHeight="min-h-48" />;
	}
	const groups = regionGroups(fleet.clusters.filter((c) => c.removedAt === undefined));
	const attention = groups.flatMap((g) => g.clusters).filter((c) => stateRank(c.state) < HEALTHY_RANK).length;
	return (
		<Page>
			<PageHeader title="System overview" />
			{groups.length === 0 ? (
				<Section>
					<EmptyState icon={<ServerIcon />} title="No clusters yet">
						Add a cluster to give Loco somewhere to run workloads.
					</EmptyState>
				</Section>
			) : (
				<>
					<p className="m-0 text-[13.5px] text-fg2">
						{attention === 0
							? "Every cluster is reporting and healthy."
							: `${pluralize(attention, "cluster")} need${attention === 1 ? "s" : ""} attention. Problems are listed first.`}
					</p>
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
