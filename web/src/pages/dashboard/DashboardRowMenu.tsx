import { createConnectQueryKey, useMutation, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createDeployment, listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { restartResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { ActivityIcon, ChevronsUpDownIcon, RotateCwIcon, ScrollTextIcon, Undo2Icon, VariableIcon } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { ResourceRowMenu } from "@/components/ResourceRowMenu";
import { Dialog } from "@/components/design/Dialog";
import { DropdownMenuItem } from "@/components/design/DropdownMenu";
import { NEEDS_WRITE, scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesKey } from "@/hooks/workspaceResources";
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
	const base = resourcePath(orgId, workspaceId, resource.id);
	const envQuery = `env=${encodeURIComponent(envName)}`;
	const obs = (view: "logs" | "events") =>
		observabilityPath(orgId, workspaceId, view, { env: envName, r: resource.name });

	const refresh = async () => {
		await Promise.all([
			queryClient.invalidateQueries({ queryKey: workspaceResourcesKey(transport, workspaceId) }),
			queryClient.invalidateQueries({
				queryKey: createConnectQueryKey({ schema: listDeployments, cardinality: "finite" }),
			}),
		]);
	};

	const rollBack = async (to: Deployment) => {
		try {
			await deploy.mutateAsync({
				resourceId: resource.id,
				region: to.region,
				spec: to.spec,
				environmentId: to.environmentId,
				commit: to.commit,
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

	return (
		<>
			<ResourceRowMenu resource={resource} workspaceId={workspaceId}>
				<DropdownMenuItem onClick={() => void navigate(obs("logs"))}>
					<ScrollTextIcon className="text-fg3" />
					View logs
				</DropdownMenuItem>
				<DropdownMenuItem onClick={() => void navigate(obs("events"))}>
					<ActivityIcon className="text-fg3" />
					View events
				</DropdownMenuItem>
				<DropdownMenuItem onClick={() => void navigate(`${base}?tab=settings&${envQuery}`)}>
					<ChevronsUpDownIcon className="text-fg3" />
					Scale
				</DropdownMenuItem>
				<DropdownMenuItem onClick={() => void navigate(`${base}?tab=variables&${envQuery}`)}>
					<VariableIcon className="text-fg3" />
					Environment variables
				</DropdownMenuItem>
				<DropdownMenuItem
					disabled={!canWrite || target === undefined}
					title={canWrite ? undefined : NEEDS_WRITE}
					onClick={() => {
						setRollingBack(true);
					}}
				>
					<Undo2Icon className="text-fg3" />
					<span className="flex-1">Roll back</span>
					{target !== undefined && <span className="font-mono text-[11.5px] text-fg3">to {depTag(target)}</span>}
				</DropdownMenuItem>
				<DropdownMenuItem
					disabled={!canWrite || last === undefined || restart.isPending}
					title={canWrite ? undefined : NEEDS_WRITE}
					onClick={() => {
						if (last !== undefined) void doRestart(last.environmentId);
					}}
				>
					<RotateCwIcon className="text-fg3" />
					Restart
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
