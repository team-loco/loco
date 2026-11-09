import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { DeploymentService } from "@gen/loco/deployment/v1/deployment_pb";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { ResourceService, ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { StepList } from "@/components/design/StepList";
import { useDeploymentWatch } from "@/hooks/useDeploymentWatch";
import { createdStep, isLive, regionDeploySteps } from "@/lib/deploy-progress";
import { getErrorMessage } from "@/lib/error-handler";
import { parseEnvText, type NewResourceForm } from "@/lib/new-resource";
import { resourcePath } from "@/lib/routes";

interface RegionRun {
	region: string;
	deploymentId: string | undefined;
	error: string | null;
}

export interface DeployRun {
	form: NewResourceForm;
	resourceId: string | undefined;
	deploying: boolean;
	regions: RegionRun[];
}

function healthCheck(path: string, defaults: DefaultServiceConfig) {
	if (path === "") return {};
	const hc = defaults.healthCheck;
	return {
		healthCheck: {
			path,
			intervalSeconds: hc?.intervalSeconds ?? 0,
			timeoutSeconds: hc?.timeoutSeconds ?? 0,
			failureThreshold: hc?.failureThreshold ?? 0,
			initialDelaySeconds: hc?.initialDelaySeconds ?? 0,
		},
	};
}

export function useServiceDeploy(workspaceId: string, env: Environment, defaults: DefaultServiceConfig) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [run, setRun] = useState<DeployRun | null>(null);
	const [createError, setCreateError] = useState<string | null>(null);

	const deploy = async (form: NewResourceForm) => {
		setCreateError(null);
		const start: DeployRun = {
			form,
			resourceId: undefined,
			deploying: true,
			regions: form.regions.map((region) => ({ region, deploymentId: undefined, error: null })),
		};
		setRun(start);
		const port = Number(form.port);
		const replicas = { cpu: form.cpu.trim(), memory: form.memory.trim(), minReplicas: form.replicas };
		try {
			const { resourceId } = await createClient(ResourceService, transport).createResource({
				workspaceId,
				name: form.name,
				type: ResourceType.SERVICE,
				...(form.isPublic
					? {
							environmentId: env.id,
							domain: {
								domainSource: DomainType.PLATFORM_PROVIDED,
								subdomain: form.subdomain,
								platformDomain: form.platformDomain,
							},
						}
					: {}),
				spec: {
					spec: {
						case: "service",
						value: {
							routing: {
								port,
								pathPrefix: defaults.routing?.pathPrefix ?? "",
								idleTimeout: defaults.routing?.idleTimeout ?? 0,
							},
							...(defaults.observability !== undefined ? { observability: defaults.observability } : {}),
							regions: Object.fromEntries(
								form.regions.map((r) => [r, { enabled: true, ...replicas, maxReplicas: form.replicas }]),
							),
							...healthCheck(form.healthPath, defaults),
						},
					},
				},
			});
			setRun({ ...start, resourceId });
			const deployments = createClient(DeploymentService, transport);
			const envVars = Object.fromEntries(parseEnvText(form.envText).vars);
			const regions = await Promise.all(
				form.regions.map(async (region): Promise<RegionRun> => {
					try {
						const res = await deployments.createDeployment({
							resourceId,
							region,
							environmentId: env.id,
							spec: {
								spec: {
									case: "service",
									value: {
										build: { type: "image", image: form.image.trim() },
										...replicas,
										maxReplicas: form.replicas,
										env: envVars,
										port,
										...healthCheck(form.healthPath, defaults),
									},
								},
							},
						});
						return { region, deploymentId: res.deploymentId, error: null };
					} catch (err) {
						return { region, deploymentId: undefined, error: getErrorMessage(err, "Couldn't start the deployment") };
					}
				}),
			);
			setRun({ ...start, resourceId, deploying: false, regions });
		} catch (err) {
			setRun(null);
			setCreateError(getErrorMessage(err, "Couldn't create the resource"));
		}
		void queryClient.invalidateQueries();
	};

	return { run, createError, deploy };
}

export function useDeployProgress(run: DeployRun, env: Environment, defaults: DefaultServiceConfig) {
	const watchFor = useDeploymentWatch(
		run.regions.flatMap((r) => (r.deploymentId === undefined ? [] : [r.deploymentId])),
	);
	const created = run.resourceId !== undefined;
	const regions = run.regions.map((r) => {
		const watch = watchFor(r.deploymentId);
		return {
			region: r.region,
			live: isLive(watch.phase),
			steps: regionDeploySteps({
				form: run.form,
				hcInterval: String(defaults.healthCheck?.intervalSeconds ?? 0),
				created,
				deploymentId: r.deploymentId,
				region: r.region,
				phase: watch.phase,
				message: watch.message,
				error: r.error,
			}),
		};
	});
	return {
		created: createdStep(run.form.name, env.name, created, run.deploying),
		regions,
		allLive: regions.length > 0 && regions.every((r) => r.live),
	};
}

export function DeployProgress({ progress }: { progress: ReturnType<typeof useDeployProgress> }) {
	return (
		<div className="flex flex-col gap-1" aria-live="polite">
			<StepList steps={[progress.created]} />
			{progress.regions.map((r) => (
				<div key={r.region} className="flex flex-col">
					<span className="pt-2 font-mono text-[12px] text-fg3">{r.region}</span>
					<StepList steps={r.steps} />
				</div>
			))}
		</div>
	);
}

export function OpenResourceButton({
	run,
	allLive,
	orgId,
	workspaceId,
}: {
	run: DeployRun;
	allLive: boolean;
	orgId: string;
	workspaceId: string;
}) {
	if (run.resourceId === undefined || run.deploying) return null;
	return (
		<Button
			variant={allLive ? "default" : "outline"}
			nativeButton={false}
			render={<Link to={resourcePath(orgId, workspaceId, run.resourceId)} />}
		>
			Open {run.form.name}
		</Button>
	);
}
