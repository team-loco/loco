import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { DeploymentService } from "@gen/loco/deployment/v1/deployment_pb";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { SecretService } from "@gen/loco/secret/v1/secret_pb";
import { ResourceService, ResourceType } from "@gen/loco/resource/v1/resource_pb";
import { getErrorMessage } from "@/lib/error-handler";
import {
	apiFieldError,
	deploymentVariables,
	healthCheckSpec,
	scalingSpec,
	type ApiFieldError,
	type NewResourceForm,
} from "@/lib/new-resource";

export interface DeployError {
	message: string;
	field: ApiFieldError | null;
}

export function useServiceDeploy(workspaceId: string, env: Environment) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [isPending, setIsPending] = useState(false);
	const [retainedResourceId, setRetainedResourceId] = useState<string>();
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
	): Promise<{ error: DeployError | null; resourceId: string | undefined }> => {
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
			const { env: envMap, secrets } = deploymentVariables(form.vars);
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
				form.regions.map(async (region): Promise<{ region: string; error: string | null; cause: unknown }> => {
					if (secretsError !== undefined) {
						const error = getErrorMessage(secretsError, "Couldn't store the secrets");
						return { region, error, cause: secretsError };
					}
					try {
						await deployments.createDeployment({
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
						return { region, error: null, cause: null };
					} catch (err) {
						return {
							region,
							error: getErrorMessage(err, "Couldn't start the deployment"),
							cause: err,
						};
					}
				}),
			);
			const failures = regions.filter((r) => r.error !== null);
			const first = failures[0];
			if (
				first !== undefined &&
				first.error !== null &&
				failures.length === regions.length &&
				(await removed(resourceId))
			) {
				return {
					error: { message: `Couldn't deploy ${form.image.trim()}: ${first.error}`, field: apiFieldError(first.cause) },
					resourceId: undefined,
				};
			}
			if (first !== undefined) {
				return {
					error: {
						message: regions.length === 1 ? first.error! : failures.map((r) => `${r.region}: ${r.error}`).join(" · "),
						field: apiFieldError(first.cause),
					},
					resourceId,
				};
			}
			return { error: null, resourceId };
		} catch (err) {
			const error = { message: getErrorMessage(err, "Couldn't create the resource"), field: apiFieldError(err) };
			return { error, resourceId: undefined };
		}
	};

	const deploy = async (form: NewResourceForm, defaults: DefaultServiceConfig) => {
		setDeployError(null);
		setIsPending(true);
		const result = await attempt(form, defaults);
		setRetainedResourceId(result.resourceId);
		setIsPending(false);
		setDeployError(result.error);
		void queryClient.invalidateQueries();
		return result;
	};

	return {
		isPending,
		resourceId: retainedResourceId,
		deployError,
		deploy,
		clearError: () => {
			if (retainedResourceId === undefined) setDeployError(null);
		},
	};
}
