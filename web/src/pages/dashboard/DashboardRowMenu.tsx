import { useMutation, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentTrigger, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { restartResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { ActivityIcon, ChevronsUpDownIcon, RotateCwIcon, ScrollTextIcon, Settings2Icon, Undo2Icon } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { ResourceRowMenu } from "@/components/ResourceRowMenu";
import { Dialog } from "@/components/design/Dialog";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { NEEDS_WRITE, scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { invalidateDeploys } from "@/hooks/workspaceDeployments";
import { deploymentRegions } from "@/lib/deployment-utils";
import { pluralize } from "@/lib/format";
import { toastConnectError } from "@/lib/error-handler";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { RollbackDialogContent } from "@/pages/resource/Dialogs";
import { buildRegions, depTag, focusRegion, lastGoodDeployment } from "@/pages/resource/model";

import type { EnvResource } from "./useDashboardData";

export function DashboardRowMenu({
	item,
	orgId,
	workspaceId,
	envName,
}: {
	item: EnvResource;
	orgId: string;
	workspaceId: string;
	envName: string;
}) {
	const { resource, last } = item;
	const navigate = useNavigate();
	const transport = useTransport();
	const queryClient = useQueryClient();
	const deploy = useMutation(createDeployment);
	const restart = useMutation(restartResource);
	const [rollingBack, setRollingBack] = useState(false);
	const canWrite = useMyScopes().resourceLevel(orgId, workspaceId, resource.id) >= scopeLevel(Scope.WRITE);
	const focus = focusRegion(buildRegions(resource, item.envDeployments, item.environment));
	const target = focus === undefined ? undefined : lastGoodDeployment(focus);
	const obs = (view: "logs" | "events") =>
		observabilityPath(orgId, workspaceId, view, { env: envName, r: [resource.slug] });

	const refresh = async () => {
		await invalidateDeploys(queryClient, transport, workspaceId, resource.id);
	};

	const rollBack = async (to: Deployment) => {
		try {
			await deploy.mutateAsync({
				resourceId: resource.id,
				regions: deploymentRegions(to),
				spec: to.spec,
				environmentId: to.environmentId,
				commit: to.commit,
				trigger: DeploymentTrigger.ROLLBACK,
				sourceDeploymentId: to.id,
			});
			setRollingBack(false);
			toast.success(`Rolling back ${resource.name} to ${depTag(to)}`);
			await refresh();
		} catch (err) {
			toastConnectError(err, `Failed to roll back ${resource.name}`);
		}
	};

	const doRestart = async (environmentId: string) => {
		try {
			await restart.mutateAsync({ resourceId: resource.id, environmentId });
			toast.success(`Restarting ${resource.name}`);
			await refresh();
		} catch (err) {
			toastConnectError(err, "Failed to restart replicas");
		}
	};

	const rollingOut = item.status === ResourceStatus.DEPLOYING;
	const settings = (tab: "settings" | "scaling") =>
		resourcePath(orgId, workspaceId, resource.id, { tab, env: envName });

	return (
		<>
			<ResourceRowMenu resource={resource} workspaceId={workspaceId} title={resource.name}>
				<DropdownMenuItem onClick={() => void navigate(obs("logs"))}>
					<ScrollTextIcon className="text-fg3" />
					View logs
				</DropdownMenuItem>
				<DropdownMenuItem onClick={() => void navigate(obs("events"))}>
					<ActivityIcon className="text-fg3" />
					View events
				</DropdownMenuItem>
				<DropdownMenuSeparator />
				<DropdownMenuItem
					disabled={!canWrite || last === undefined || rollingOut || restart.isPending}
					title={canWrite ? undefined : NEEDS_WRITE}
					onClick={() => {
						if (last !== undefined) void doRestart(last.environmentId);
					}}
				>
					<RotateCwIcon className="text-fg3" />
					<span className="flex-1">Restart</span>
					<span className="font-mono text-[11.5px] text-fg3">
						{rollingOut ? "rollout in progress" : pluralize(item.totalReplicas, "replica")}
					</span>
				</DropdownMenuItem>
				<DropdownMenuItem onClick={() => void navigate(settings("scaling"))}>
					<ChevronsUpDownIcon className="text-fg3" />
					Scale
				</DropdownMenuItem>
				{target !== undefined && (
					<DropdownMenuItem
						disabled={!canWrite}
						title={canWrite ? undefined : NEEDS_WRITE}
						onClick={() => {
							setRollingBack(true);
						}}
					>
						<Undo2Icon className="text-fg3" />
						<span className="flex-1">Roll back</span>
						<span className="font-mono text-[11.5px] text-fg3">to {depTag(target)}</span>
					</DropdownMenuItem>
				)}
				<DropdownMenuSeparator />
				<DropdownMenuItem onClick={() => void navigate(settings("settings"))}>
					<Settings2Icon className="text-fg3" />
					Settings
				</DropdownMenuItem>
			</ResourceRowMenu>
			<Dialog
				open={rollingBack}
				onOpenChange={(open: boolean) => {
					if (!open) setRollingBack(false);
				}}
			>
				{rollingBack && target !== undefined && (
					<RollbackDialogContent
						resourceName={resource.name}
						target={target}
						current={focus?.current}
						pending={deploy.isPending}
						onConfirm={() => void rollBack(target)}
					/>
				)}
			</Dialog>
		</>
	);
}
