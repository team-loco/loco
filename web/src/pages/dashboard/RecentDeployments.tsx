import { RocketIcon } from "lucide-react";
import { useNavigate } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { EmptyState } from "@/components/design/EmptyState";
import { Section } from "@/components/design/Page";
import { DeploymentPhaseBadge } from "@/components/design/StatusBadge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { imageVersion } from "@/lib/image";
import { resourcePath } from "@/lib/routes";
import { tsMs } from "@/lib/time";

import { deploymentImage, startedLabel } from "./format";

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

	return (
		<Section title="Recent deployments">
			{recent.length === 0 ? (
				<EmptyState icon={<RocketIcon />} title={`No deployments to ${envName} in the last 24 hours`} />
			) : (
				<Table className="min-w-[720px] table-fixed">
					<TableHeader>
						<TableRow>
							<TableHead className="w-[112px]">Started</TableHead>
							<TableHead className="w-[112px]">Status</TableHead>
							<TableHead className="w-[142px]">Resource</TableHead>
							<TableHead className="w-[112px]">Region</TableHead>
							<TableHead className="w-[112px]">Image</TableHead>
							<TableHead>Message</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{recent.map(({ deployment: d, resource }) => {
							const href = resourcePath(orgId, workspaceId, resource.id);
							return (
								<TableRow key={d.id} className="cursor-pointer hover:bg-panel2" onClick={() => void navigate(href)}>
									<TableCell className="text-fg2 tabular-nums">{startedLabel(tsMs(d.createdAt))}</TableCell>
									<TableCell>
										<DeploymentPhaseBadge phase={d.status} />
									</TableCell>
									<TableCell className="truncate font-medium">{resource.name}</TableCell>
									<TableCell className="text-fg2">{d.region}</TableCell>
									<TableCell className="truncate">{imageVersion(deploymentImage(d))}</TableCell>
									<TableCell className="truncate text-fg2">{d.message}</TableCell>
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
			)}
		</Section>
	);
}
