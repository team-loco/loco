import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { CircleAlertIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { DeploymentService } from "@gen/loco/deployment/v1/deployment_pb";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { ResourceService, ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { CodeBlock } from "@/components/design/CodeBlock";
import { StepList } from "@/components/design/StepList";
import { useDeploymentWatch } from "@/hooks/useDeploymentWatch";
import { deployOutcome, progressSteps } from "@/lib/deploy-progress";
import { getErrorMessage } from "@/lib/error-handler";
import { errorCount, locoYaml, parseEnvText, type FormErrors, type NewResourceForm } from "@/lib/new-resource";
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

export function SidePanel({
	form,
	errors,
	env,
	defaults,
	schemaUrl,
	orgId,
	workspaceId,
	run,
	onRun,
	tried,
	onTried,
}: {
	form: NewResourceForm;
	errors: FormErrors;
	env: Environment;
	defaults: DefaultServiceConfig;
	schemaUrl: string;
	orgId: string;
	workspaceId: string;
	run: DeployRun | null;
	onRun: (run: DeployRun | null) => void;
	tried: boolean;
	onTried: () => void;
}) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [createError, setCreateError] = useState<string | null>(null);
	const watchFor = useDeploymentWatch(
		run?.regions.flatMap((r) => (r.deploymentId === undefined ? [] : [r.deploymentId])) ?? [],
	);
	const shownForm = run?.form ?? form;
	const vars = parseEnvText(shownForm.envText).vars;
	const port = Number(shownForm.port);

	const deploy = async () => {
		onTried();
		if (errorCount(errors) > 0) return;
		setCreateError(null);
		const start: DeployRun = {
			form,
			resourceId: undefined,
			deploying: true,
			regions: form.regions.map((region) => ({ region, deploymentId: undefined, error: null })),
		};
		onRun(start);
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
			onRun({ ...start, resourceId });
			const deployments = createClient(DeploymentService, transport);
			const env_vars = Object.fromEntries(parseEnvText(form.envText).vars);
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
										env: env_vars,
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
			onRun({ ...start, resourceId, deploying: false, regions });
		} catch (err) {
			onRun(null);
			setCreateError(getErrorMessage(err, "Couldn't create the resource"));
		}
		void queryClient.invalidateQueries();
	};

	const target = {
		name: shownForm.name,
		image: shownForm.image.trim(),
		min: shownForm.replicas,
		cpu: shownForm.cpu,
		memory: shownForm.memory,
		hcPath: shownForm.healthPath,
		hcInterval: String(defaults.healthCheck?.intervalSeconds ?? 0),
		resourceId: run?.resourceId,
	};
	const regionSteps = (run?.regions ?? []).map((r) => {
		const watch = watchFor(r.deploymentId);
		return {
			region: r.region,
			live: deployOutcome(watch.phase) === "live",
			steps: progressSteps({
				target: { ...target, deploymentId: r.deploymentId },
				region: r.region,
				envName: env.name,
				phase: watch.phase,
				message: watch.message,
				error: r.error,
				submitting: run?.deploying ?? false,
			}),
		};
	});
	const allLive = regionSteps.length > 0 && regionSteps.every((r) => r.live);
	const created = regionSteps[0]?.steps[0];
	const summary: [string, string][] = [
		["name", shownForm.name === "" ? "—" : shownForm.name],
		["image", shownForm.image.trim() === "" ? "—" : shownForm.image.trim()],
		["regions", shownForm.regions.length === 0 ? "—" : shownForm.regions.join(", ")],
		["replicas", `${shownForm.replicas.toString()} per region`],
		["size", `${shownForm.cpu} CPU · ${shownForm.memory}`],
		[
			"address",
			!shownForm.isPublic
				? "internal only"
				: shownForm.subdomain === "" || shownForm.platformDomain === ""
					? "—"
					: `https://${shownForm.subdomain}.${shownForm.platformDomain}`,
		],
		["variables", vars.length.toString()],
	];
	const invalid = errorCount(errors);
	const yaml = locoYaml({ schemaUrl, form: shownForm });

	return (
		<aside className="flex min-w-0 flex-col gap-3 min-[980px]:sticky min-[980px]:top-4">
			<div className="flex flex-col gap-3.5 rounded-[10px] bg-panel px-5 py-[18px]">
				<div className="flex items-center justify-between gap-2.5">
					<h2 className="m-0 text-[15px] font-medium">Summary</h2>
					<span className="font-mono text-[12px] text-fg3">{env.name}</span>
				</div>
				<dl className="m-0 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-5 gap-y-2.5 font-mono text-[12px]">
					{summary.map(([k, v]) => (
						<div key={k} className="contents">
							<dt className="text-fg3">{k}</dt>
							<dd className="m-0 min-w-0 wrap-anywhere">{v}</dd>
						</div>
					))}
				</dl>
				{run === null ? (
					<>
						<Button
							size="lg"
							className="h-[38px]"
							onClick={() => {
								void deploy();
							}}
						>
							{form.name === "" ? "Deploy" : `Deploy ${form.name}`}
						</Button>
						{tried && invalid > 0 && (
							<span role="alert" className="flex items-center justify-center gap-1.5 text-[12.5px] text-bad-fg">
								<CircleAlertIcon className="size-[13px]" />
								Fix {invalid.toString()} {invalid === 1 ? "field" : "fields"} to deploy
							</span>
						)}
						{createError !== null && (
							<span role="alert" className="text-[12.5px] text-bad-fg">
								{createError}
							</span>
						)}
					</>
				) : (
					<div className="flex flex-col gap-1 border-t border-line2 pt-2" aria-live="polite">
						{created !== undefined && <StepList steps={[created]} />}
						{regionSteps.map((r) => (
							<div key={r.region} className="flex flex-col">
								<span className="pt-2 font-mono text-[12px] text-fg3">{r.region}</span>
								<StepList steps={r.steps.slice(1)} />
							</div>
						))}
						{run.resourceId !== undefined && !run.deploying && (
							<Button
								size="lg"
								variant={allLive ? "default" : "outline"}
								className="mt-2 h-[38px]"
								nativeButton={false}
								render={<Link to={resourcePath(orgId, workspaceId, run.resourceId)} />}
							>
								Open {shownForm.name}
							</Button>
						)}
					</div>
				)}
			</div>
			<div className="flex flex-col gap-2.5 rounded-[10px] bg-panel px-5 py-[18px]">
				<h2 className="m-0 text-[15px] font-medium">loco.yaml</h2>
				<p className="m-0 text-[12.5px] text-fg3">
					The same resource as a <code className="font-mono">loco.yaml</code>. Save it next to your code and run:
				</p>
				<CodeBlock>{`loco deploy --env ${env.name}`}</CodeBlock>
				<CodeBlock className="max-h-[360px]">{yaml}</CodeBlock>
			</div>
		</aside>
	);
}
