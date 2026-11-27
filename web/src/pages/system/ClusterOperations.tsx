import { maybeTsMs } from "@/lib/time";
import { useQuery } from "@connectrpc/connect-query";
import { getClusterOperations } from "@gen/loco/cluster/v1/cluster-ClusterAdminService_connectquery";
import type { ClusterWorkloadAttention } from "@gen/loco/cluster/v1/cluster_pb";
import { Link } from "react-router";

import { Badge } from "@/components/design/Badge";
import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { buildLogsPath, logsAtHref, resourcePath } from "@/lib/routes";
import { auditActor } from "@/pages/settings/auditEvents";

import { SystemTime } from "./shared";

export function workloadPath(workload: ClusterWorkloadAttention): string {
	return workload.kind === "build"
		? logsAtHref(buildLogsPath(workload.orgId, workload.workspaceId, workload.id), maybeTsMs(workload.updatedAt))
		: resourcePath(workload.orgId, workload.workspaceId, workload.resourceId, {
				env: workload.environmentSlug,
			});
}

export function ClusterOperations({
	clusterId,
	section,
	healthMessage = "",
}: {
	clusterId: string;
	section: "issues" | "events";
	healthMessage?: string;
}) {
	const title = section === "issues" ? "Issues" : "Events";
	const healthIssue = section === "issues" && healthMessage !== "";
	const health = healthIssue ? <p className="m-0 px-5 py-4 text-[13px] text-fg2">{healthMessage}</p> : null;
	const result = useQuery(getClusterOperations, { clusterId }, { refetchInterval: 30_000 });
	if (result.isLoading)
		return (
			<Section title={title}>
				{health}
				<Skeleton className="m-5 h-40" />
			</Section>
		);
	if (result.error !== null)
		return (
			<Section title={title}>
				{health}
				<div className="p-5">
					<ErrorState
						error={result.error}
						what={`cluster ${section}`}
						rpc={getClusterOperations}
						onRetry={async () => await result.refetch()}
					/>
				</div>
			</Section>
		);
	const workloads = result.data?.workloads ?? [];
	const events = result.data?.events ?? [];
	if (section === "issues")
		return (
			<Section
				title="Issues"
				count={result.data?.hasMoreWorkloads ? "50+" : String(workloads.length + Number(healthIssue))}
			>
				{health}
				{workloads.length === 0 ? (
					!healthIssue && <p className="m-0 px-5 py-5 text-[13px] text-fg3">No issues</p>
				) : (
					<ul className="m-0 list-none divide-y divide-line p-0">
						{workloads.map((workload) => (
							<li
								key={`${workload.kind}/${workload.id}`}
								className="flex flex-wrap items-start gap-x-5 gap-y-2 px-5 py-4"
							>
								<div className="min-w-0 flex-1 basis-64">
									<Link
										to={workloadPath(workload)}
										className="text-[13.5px] font-medium underline decoration-line2 underline-offset-4 hover:decoration-current"
									>
										{workload.resourceName}
									</Link>
									<span className="ml-2 text-[12px] text-fg3">
										{workload.kind} · {workload.environmentSlug}
									</span>
									{workload.message !== "" && (
										<p className="mt-1 mb-0 break-words text-[12.5px] text-fg2">{workload.message}</p>
									)}
								</div>
								<Badge tone="warn" size="sm">
									{workload.status}
								</Badge>
								<SystemTime ts={workload.updatedAt} />
							</li>
						))}
					</ul>
				)}
			</Section>
		);
	return (
		<Section
			title="Events"
			actions={
				<Link
					to={`/system/audit?q=${encodeURIComponent(`@target:${clusterId}`)}`}
					className="text-[12.5px] text-fg2 underline underline-offset-4"
				>
					Audit log
				</Link>
			}
		>
			{events.length === 0 ? (
				<p className="m-0 px-5 py-5 text-[13px] text-fg3">No events</p>
			) : (
				<ul className="m-0 list-none divide-y divide-line p-0">
					{events.map((event) => (
						<li key={event.id} className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1 px-5 py-3">
							<div className="min-w-0">
								<p className="m-0 text-[13px]">{event.title || event.type}</p>
								<p className="mt-1 mb-0 text-[12px] text-fg3">{auditActor(event).name}</p>
							</div>
							<SystemTime ts={event.createdAt} />
						</li>
					))}
				</ul>
			)}
		</Section>
	);
}
