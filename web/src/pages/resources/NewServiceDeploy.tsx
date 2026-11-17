import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ExternalLinkIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { DeploymentService, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { SecretService } from "@gen/loco/secret/v1/secret_pb";
import { ResourceService, ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { SheetBody, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { StatusGlyph, type Glyph } from "@/components/design/StatusGlyph";
import { Banner } from "@/components/design/Banner";
import { useNow } from "@/hooks/useNow";
import { isInFlight, shortSha } from "@/lib/deployment-utils";
import { formatBuildDuration } from "@/pages/resource/builds";
import { deploymentElapsedMs, deploymentStatusLabel, showDeploymentLogs } from "@/pages/resource/deployment-progress";
import { DeploymentRollout } from "@/pages/resource/DeploymentRollout";
import { useDeploymentWatch } from "@/hooks/useDeploymentWatch";
import { runOutcome, type RunOutcome } from "@/lib/deploy-progress";
import { getErrorMessage } from "@/lib/error-handler";
import {
	apiFieldError,
	deploymentVariables,
	healthCheckSpec,
	scalingSpec,
	type ApiFieldError,
	type NewResourceForm,
} from "@/lib/new-resource";
import { observabilityPath, resourcePath } from "@/lib/routes";

interface RegionRun {
	region: string;
	deploymentId: string | undefined;
	error: string | null;
}

interface DeployRun {
	form: Omit<NewResourceForm, "vars">;
	resourceId: string | undefined;
	deploying: boolean;
	regions: RegionRun[];
}

export interface DeployError {
	message: string;
	field: ApiFieldError | null;
}

export function useServiceDeploy(workspaceId: string, env: Environment) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [run, setRun] = useState<DeployRun | null>(null);
	const [deployError, setDeployError] = useState<DeployError | null>(null);

	const removed = async (resourceId: string) => {
		try {
			await createClient(ResourceService, transport).deleteResource({ resourceId });
			return true;
		} catch {
			return false;
		}
	};

	const attempt = async (
		form: NewResourceForm,
		defaults: DefaultServiceConfig,
	): Promise<{ error: DeployError | null; deployed: boolean }> => {
		const { vars, ...summary } = form;
		const start: DeployRun = {
			form: summary,
			resourceId: undefined,
			deploying: true,
			regions: form.regions.map((region) => ({ region, deploymentId: undefined, error: null })),
		};
		setRun(start);
		const port = form.port.trim() === "" ? undefined : Number(form.port);
		const replicas = { cpu: form.cpu.trim(), memory: form.memory.trim(), ...scalingSpec(form) };
		const healthCheck = healthCheckSpec(form.health, defaults.healthCheck?.initialDelaySeconds ?? 0);
		const health = healthCheck === undefined ? {} : { healthCheck };
		try {
			const deployments = createClient(DeploymentService, transport);
			const { image } = await deployments.resolveImage({ workspaceId, image: form.image.trim() });
			const { resourceId } = await createClient(ResourceService, transport).createResource({
				workspaceId,
				name: form.name,
				type: ResourceType.SERVICE,
				...(form.description.trim() === "" ? {} : { description: form.description.trim() }),
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
							routing: { port },
							regions: Object.fromEntries(form.regions.map((r) => [r, { enabled: true, ...replicas }])),
							...health,
						},
					},
				},
			});
			setRun({ ...start, resourceId });
			const { env: envMap, secrets } = deploymentVariables(vars);
			const secretNames = Object.keys(secrets);
			let secretsError: unknown = undefined;
			if (secretNames.length > 0) {
				try {
					await createClient(SecretService, transport).setSecrets({
						resourceId,
						environmentId: env.id,
						values: secrets,
					});
				} catch (error) {
					secretsError = error;
				}
			}
			const regions = await Promise.all(
				form.regions.map(async (region): Promise<RegionRun & { cause: unknown }> => {
					if (secretsError !== undefined) {
						const error = getErrorMessage(secretsError, "Couldn't store the secrets");
						return { region, deploymentId: undefined, error, cause: secretsError };
					}
					try {
						const res = await deployments.createDeployment({
							resourceId,
							region,
							environmentId: env.id,
							secretBindings: { names: secretNames },
							spec: {
								spec: {
									case: "service",
									value: {
										build: { type: "image", image },
										...replicas,
										env: envMap,
										port,
										...health,
									},
								},
							},
						});
						return { region, deploymentId: res.deploymentId, error: null, cause: null };
					} catch (err) {
						return {
							region,
							deploymentId: undefined,
							error: getErrorMessage(err, "Couldn't start the deployment"),
							cause: err,
						};
					}
				}),
			);
			const first = regions[0];
			if (
				first !== undefined &&
				first.error !== null &&
				regions.every((r) => r.deploymentId === undefined) &&
				(await removed(resourceId))
			) {
				return {
					error: { message: `Couldn't deploy ${form.image.trim()}: ${first.error}`, field: apiFieldError(first.cause) },
					deployed: false,
				};
			}
			setRun({ ...start, resourceId, deploying: false, regions });
			return { error: null, deployed: regions.some((r) => r.deploymentId !== undefined) };
		} catch (err) {
			const error = { message: getErrorMessage(err, "Couldn't create the resource"), field: apiFieldError(err) };
			return { error, deployed: false };
		}
	};

	const deploy = async (form: NewResourceForm, defaults: DefaultServiceConfig) => {
		setDeployError(null);
		const result = await attempt(form, defaults);
		if (result.error !== null) setRun(null);
		setDeployError(result.error);
		void queryClient.invalidateQueries();
		return result;
	};

	return {
		run,
		deployError,
		deploy,
		clearError: () => {
			setDeployError(null);
		},
	};
}

type DrawerState = "draft" | RunOutcome;

const DRAWER_BADGE: Record<DrawerState, { label: string; glyph: Glyph }> = {
	draft: { label: "Draft", glyph: "draft" },
	deploying: { label: "Deploying", glyph: "spin" },
	live: { label: "Live", glyph: "ok" },
	failed: { label: "Failed", glyph: "err" },
};

export function DrawerHeader({ name, image, state }: { name: string; image: string; state: DrawerState }) {
	const badge = DRAWER_BADGE[state];
	return (
		<SheetHeader>
			<div className="flex min-w-0 flex-col gap-0.5">
				<div className="flex min-w-0 items-center gap-2.5">
					<SheetTitle className="truncate">{name}</SheetTitle>
					<span className="flex shrink-0 items-center gap-1.5 text-[12.5px] text-fg2">
						<StatusGlyph glyph={badge.glyph} />
						{badge.label}
					</span>
				</div>
				{image !== "" && (
					<span className="truncate font-mono text-[12px] text-fg3" title={image}>
						{image}
					</span>
				)}
			</div>
		</SheetHeader>
	);
}

function useDeployProgress(run: DeployRun) {
	const watchFor = useDeploymentWatch(
		run.regions.flatMap((r) => (r.deploymentId === undefined ? [] : [r.deploymentId])),
	);
	const regions = run.regions.map((r) => ({ ...r, watch: watchFor(r.deploymentId) }));
	return { regions, outcome: runOutcome(regions.map((r) => ({ phase: r.watch.deployment?.status, error: r.error }))) };
}

function DeploymentHeading({ deployment }: { deployment: Deployment }) {
	const now = useNow(isInFlight(deployment.status) ? 1000 : undefined).getTime();
	const elapsed = deploymentElapsedMs(deployment, now);
	return (
		<div className="flex flex-wrap items-baseline justify-between gap-2 text-[13px]">
			<span role="status" className="font-medium">
				{deploymentStatusLabel(deployment)}
			</span>
			{elapsed !== undefined && (
				<span className="font-mono text-[12px] text-fg3">Elapsed {formatBuildDuration(elapsed)}</span>
			)}
			{deployment.commit?.sha && (
				<span className="basis-full font-mono text-[12px] text-fg3">{shortSha(deployment.commit.sha)}</span>
			)}
		</div>
	);
}

function progressTitle(run: DeployRun, outcome: RunOutcome): string {
	if (outcome === "live") return "Live";
	if (outcome === "failed") return `Couldn't deploy ${run.form.name}`;
	return run.resourceId === undefined ? `Creating ${run.form.name}` : `Deploying ${run.form.name}`;
}

export function DeployRunView({
	run,
	env,
	orgId,
	workspaceId,
	onDone,
}: {
	run: DeployRun;
	env: Environment;
	orgId: string;
	workspaceId: string;
	onDone: () => void;
}) {
	const { regions, outcome } = useDeployProgress(run);
	const logsHref = observabilityPath(orgId, workspaceId, "logs", { env: env.name, r: [run.form.name] });
	const host = `${run.form.subdomain}.${run.form.platformDomain}`;
	return (
		<>
			<DrawerHeader name={run.form.name} image={run.form.image.trim()} state={outcome} />
			<SheetBody className="gap-1">
				<div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 pt-1">
					<span className="text-[15px] font-medium">{progressTitle(run, outcome)}</span>
					{outcome === "live" && run.form.isPublic && (
						<a
							href={`https://${host}`}
							target="_blank"
							rel="noreferrer"
							className="flex items-center gap-1 font-mono text-[12.5px] text-fg2 hover:text-foreground"
						>
							{host}
							<ExternalLinkIcon className="size-3.5" />
						</a>
					)}
				</div>
				<div className="flex flex-col gap-4 pt-3">
					{regions.map((r) => (
						<section key={r.region} aria-label={r.region} className="flex min-w-0 flex-col gap-3">
							{regions.length > 1 && <span className="pt-2 font-mono text-[12px] text-fg3">{r.region}</span>}
							{r.error !== null ? (
								<Banner title="Failed" className="mx-0 mt-0">
									{r.error}
								</Banner>
							) : r.watch.deployment === undefined ? (
								<p role="status" className="m-0 text-[13px] text-fg3">
									{run.resourceId === undefined ? "Creating service…" : "Waiting for deployment…"}
								</p>
							) : (
								<>
									<DeploymentHeading deployment={r.watch.deployment} />
									<DeploymentRollout deployment={r.watch.deployment} activity={r.watch.activity} logsHref={logsHref} />
									{showDeploymentLogs(logsHref, r.watch.deployment) && (
										<Button variant="outline" nativeButton={false} render={<Link to={logsHref} />}>
											View logs
										</Button>
									)}
								</>
							)}
							{r.watch.error !== undefined && (
								<p role="status" className="m-0 text-[12px] text-warn-fg">
									Disconnected. Reconnecting… {r.watch.error}
								</p>
							)}
						</section>
					))}
				</div>
			</SheetBody>
			<SheetFooter className="flex-wrap items-center">
				<span className="min-w-0 flex-1 text-[12.5px] text-fg3 max-sm:basis-full">
					To <span className="font-mono text-fg2">{env.name}</span>
				</span>
				{run.resourceId !== undefined && !run.deploying && (
					<Button
						variant="outline"
						nativeButton={false}
						render={<Link to={resourcePath(orgId, workspaceId, run.resourceId)} />}
					>
						Open {run.form.name}
					</Button>
				)}
				<Button onClick={onDone}>Done</Button>
			</SheetFooter>
		</>
	);
}
