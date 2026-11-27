import type { ReactNode } from "react";
import { useParams } from "react-router";

import { ErrorCard } from "@/components/ErrorCard";
import { CopyId } from "@/components/design/CopyId";
import { InlineCode } from "@/components/design/InlineCode";
import { Page, PageHeader, PageLoading, Section } from "@/components/design/Page";
import { useMyScopes } from "@/hooks/useMyScopes";
import { notFound } from "@/lib/not-found";
import { ROLE_LABEL, systemAccess } from "@/lib/system";

import { ClusterNodes } from "./ClusterNodes";
import { ClusterOperations } from "./ClusterOperations";
import { ClusterActionButtons } from "./ClusterActions";
import { AgentVersion } from "./ClusterTable";
import { ClusterStateBadge, SystemTime, useFleet } from "./shared";

function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<dt className="text-[12.5px] text-fg3">{label}</dt>
			<dd className="m-0 min-w-0 text-[13.5px]">{children}</dd>
		</div>
	);
}

export function SystemClusterDetail() {
	const { clusterId = "" } = useParams();
	const fleet = useFleet();
	const canChange = systemAccess(useMyScopes().scopes) === "admin";
	if (fleet.isLoading) return <PageLoading />;
	if (fleet.error !== null) {
		return <ErrorCard error={fleet.error} fallbackMessage="Failed to load the cluster" minHeight="min-h-48" />;
	}
	const cluster = fleet.clusters.find((c) => c.id === clusterId);
	if (cluster === undefined) notFound();
	const state = fleet.stateOf(cluster);
	return (
		<Page>
			<PageHeader
				title={
					<>
						<span className="font-mono">{cluster.name}</span>
						<ClusterStateBadge state={state} />
					</>
				}
				actions={
					canChange && (
						<div className="flex flex-wrap items-center gap-2">
							<ClusterActionButtons cluster={cluster} state={state} />
						</div>
					)
				}
			/>
			<Section title="Details">
				<dl className="m-0 grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-x-6 gap-y-5 p-5">
					<Fact label="Region">
						<InlineCode>{cluster.region}</InlineCode>
					</Fact>
					<Fact label="Role">{ROLE_LABEL[cluster.role]}</Fact>
					<Fact label="Agent version">
						<AgentVersion cluster={cluster} apiVersion={fleet.apiVersion} />
					</Fact>
					<Fact label="Key fingerprint">
						{cluster.agentKeyFingerprint === undefined ? (
							<span className="text-fg3">Not enrolled</span>
						) : (
							<CopyId value={cluster.agentKeyFingerprint} className="-ml-1.5">
								<span className="font-mono text-[12.5px]">{cluster.agentKeyFingerprint.slice(0, 16)}…</span>
							</CopyId>
						)}
					</Fact>
					<Fact label="Last heartbeat">
						<SystemTime ts={cluster.lastHeartbeat} />
					</Fact>
					{cluster.removedAt !== undefined && (
						<Fact label="Removed">
							<SystemTime ts={cluster.removedAt} />
						</Fact>
					)}
					<Fact label="Domains">
						{cluster.domains.length === 0 ? (
							<span className="text-fg3">None</span>
						) : (
							<span className="flex flex-wrap gap-1.5">
								{cluster.domains.map((domain) => (
									<InlineCode key={domain}>{domain}</InlineCode>
								))}
							</span>
						)}
					</Fact>
					<Fact label="Cluster ID">
						<CopyId value={cluster.id} className="-ml-1.5 max-w-full">
							<span className="truncate font-mono text-[12.5px]">{cluster.id}</span>
						</CopyId>
					</Fact>
				</dl>
			</Section>
			<ClusterOperations clusterId={cluster.id} section="issues" healthMessage={cluster.healthMessage} />

			<ClusterNodes key={cluster.id} clusterId={cluster.id} />
			<ClusterOperations clusterId={cluster.id} section="events" />
		</Page>
	);
}
