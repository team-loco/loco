import { RocketIcon } from "lucide-react";
import { useNavigate } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { EmptyState } from "@/components/design/EmptyState";
import { Section } from "@/components/design/Page";
import { DeploymentPhaseBadge } from "@/components/design/StatusBadge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { resourcePath } from "@/lib/routes";
import { tsMs } from "@/lib/time";

import { deploymentImage, imageTag, startedLabel } from "./format";

const WINDOW_MS = 24 * 60 * 60 * 1000;
const LIMIT = 10;

export function RecentDeployments({
	deployments,
	orgId,
	workspaceId,
	envName,
	nowMs,
}: {
	deployments: { deployment: Deployment; resource: Resource }[];
	orgId: string;
	workspaceId: string;
	envName: string;
	nowMs: number;
}) {
	const navigate = useNavigate();
	const recent = deployments.filter((d) => nowMs - tsMs(d.deployment.createdAt) <= WINDOW_MS).slice(0, LIMIT);
	const head = "h-10 px-0 text-sm font-semibold text-fg2";

	return (
		<Section title="Recent deployments">
			{recent.length === 0 ? (
				<EmptyState icon={<RocketIcon />} title={`No deployments to ${envName} in the last 24 hours`} />
			) : (
				<Table className="min-w-[720px] table-fixed text-base">
					<TableHeader>
						<TableRow className="border-line hover:bg-transparent">
							<TableHead className={`${head} w-[112px] pl-4`}>Started</TableHead>
							<TableHead className={`${head} w-[112px]`}>Status</TableHead>
							<TableHead className={`${head} w-[142px]`}>Resource</TableHead>
							<TableHead className={`${head} w-[112px]`}>Region</TableHead>
							<TableHead className={`${head} w-[112px]`}>Image</TableHead>
							<TableHead className={`${head} pr-4`}>Message</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{recent.map(({ deployment: d, resource }) => {
							const href = resourcePath(orgId, workspaceId, resource.id);
							return (
								<TableRow
									key={d.id}
									className="h-11 cursor-pointer border-line hover:bg-bg2"
									onClick={() => void navigate(href)}
								>
									<TableCell className="py-0 pr-3 pl-4 text-fg2 tabular-nums">
										{startedLabel(tsMs(d.createdAt))}
									</TableCell>
									<TableCell className="py-0 pr-3 pl-0">
										<DeploymentPhaseBadge phase={d.status} />
									</TableCell>
									<TableCell className="truncate py-0 pr-3 pl-0 font-semibold">{resource.name}</TableCell>
									<TableCell className="py-0 pr-3 pl-0 text-fg2">{d.region}</TableCell>
									<TableCell className="truncate py-0 pr-3 pl-0">{imageTag(deploymentImage(d))}</TableCell>
									<TableCell className="truncate py-0 pr-4 pl-0 text-fg2">{d.message}</TableCell>
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
			)}
		</Section>
	);
}
