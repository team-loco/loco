import { create, fromJson, toJson } from "@bufbuild/protobuf";
import { createConnectQueryKey, useQuery } from "@connectrpc/connect-query";
import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Draft, DraftDeployment } from "@/lib/drafts";
import { useCreateDeployment } from "@/hooks/useCreateDeployment";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { CreateDeploymentRequestSchema, DeploymentService } from "@gen/loco/deployment/v1/deployment_pb";
import { DomainService, DomainType } from "@gen/loco/domain/v1/domain_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { ResourceService, ResourceType } from "@gen/loco/resource/v1/resource_pb";
import { DOMAIN_AVAILABILITY_KEY } from "@/hooks/useDomainAvailability";
import { invalidateDeploys } from "@/hooks/workspaceDeployments";
import { deploymentOutcomeUnknown } from "@/lib/deployment-request";
import { getErrorMessage } from "@/lib/error-handler";
import {
	apiFieldError,
	commandEntries,
	envVars,
	healthCheckSpec,
	scalingSpec,
	type ApiFieldError,
	type NewResourceForm,
} from "@/lib/new-resource";

export interface DeployError {
	message: string;
	field: ApiFieldError | null;
}

export function useServiceDeploy(
	workspaceId: string,
	env: Environment,
	draft: Draft | undefined,
	save: (deployment: DraftDeployment | null, form?: NewResourceForm) => boolean,
) {
	const transport = useTransport();
	const deployment = useCreateDeployment();
	const queryClient = useQueryClient();
	const [isPending, setIsPending] = useState(false);
	const pendingResource = draft?.deployment;
	const owned = useQuery(
		getResource,
		{
			key: {
				case: "resourceId",
				value: pendingResource?.request.resourceId ?? "",
			},
		},
		{ enabled: pendingResource !== undefined },
	);
	const [deployError, setDeployError] = useState<DeployError | null>(null);

	const removed = async (resourceId: string) => {
		try {
			await createClient(ResourceService, transport).deleteResource({
				resourceId,
			});
			return true;
		} catch {
			return false;
		}
	};

	const attempt = async (
		form: NewResourceForm,
		defaults: DefaultServiceConfig,
	): Promise<{
		error: DeployError | null;
		resourceId: string | undefined;
	}> => {
		const port = form.port.trim() === "" ? undefined : Number(form.port);
		const replicas = {
			cpu: form.cpu.trim(),
			memory: form.memory.trim(),
			...scalingSpec(form),
		};
		const healthCheck = healthCheckSpec(form.health, defaults.healthCheck?.initialDelaySeconds ?? 0);
		const health = healthCheck === undefined ? {} : { healthCheck };
		let resourceId = pendingResource?.request.resourceId;
		try {
			const deployments = createClient(DeploymentService, transport);
			const formKey = JSON.stringify(form);
			const previous = pendingResource;
			const priorRequest =
				previous === undefined ? undefined : fromJson(CreateDeploymentRequestSchema, previous.request);
			const savedImage =
				priorRequest?.spec?.spec.case === "service" ? priorRequest.spec.spec.value.build?.image : undefined;
			const image =
				previous?.form === formKey && savedImage !== undefined
					? savedImage
					: (
							await deployments.resolveImage({
								workspaceId,
								image: form.image.trim(),
							})
						).image;
			if (resourceId === undefined) {
				const created = await createClient(ResourceService, transport).createResource({
					workspaceId,
					name: form.name,
					type: ResourceType.SERVICE,
					...(form.description.trim() === "" ? {} : { description: form.description.trim() }),
					...(form.isPublic
						? {
								environmentId: env.id,
								domain: {
									domainSource: DomainType.APP,
									subdomain: form.subdomain,
									appDomain: form.appDomain,
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
				resourceId = created.resourceId;
			}
			const resources = createClient(ResourceService, transport);
			const { resource } = await resources.getResource({ key: { case: "resourceId", value: resourceId } });
			if (resource === undefined) throw new Error("The created resource was not returned");

			const envMap = Object.fromEntries(envVars(form.vars));
			const request =
				previous?.form === formKey && priorRequest !== undefined
					? priorRequest
					: create(CreateDeploymentRequestSchema, {
							requestId: crypto.randomUUID(),
							updateResourceTargets: true,
							resourceRevision: resource.revision,
							resourceId,
							regions: form.regions,
							environmentId: env.id,
							removeHealthCheck: !form.health.enabled,
							spec: {
								spec: {
									case: "service",
									value: {
										build: { type: "image", image },
										regions: Object.fromEntries(form.regions.map((r) => [r, replicas])),
										env: envMap,
										port,
										command: commandEntries(form.command),
										args: commandEntries(form.args),
										...health,
									},
								},
							},
						});
			const snapshot: DraftDeployment = {
				form: formKey,
				request: {
					...toJson(CreateDeploymentRequestSchema, request),
					requestId: request.requestId,
					resourceId: request.resourceId,
				},
			};
			if (!save(snapshot, form))
				return {
					error: {
						message: "Couldn't save the deployment attempt. It was not sent.",
						field: null,
					},
					resourceId,
				};
			const metadataChanged = resource.name !== form.name || resource.description !== form.description.trim();
			if (metadataChanged) {
				await resources.updateResource({
					resourceId,
					name: form.name,
					description: form.description.trim(),
					revision: resource.revision,
				});
			}
			const domains = createClient(DomainService, transport);
			const currentDomains = resource.domains.filter((domain) => domain.environmentId === env.id);
			if (!form.isPublic) {
				for (const domain of currentDomains.toSorted((a, b) => Number(a.isPrimary) - Number(b.isPrimary))) {
					await domains.deleteResourceDomain({ domainId: domain.id });
				}
			}
			try {
				await deployment.mutateAsync(request);
			} catch (cause) {
				const message = getErrorMessage(cause, "Couldn't start the deployment");
				const field = apiFieldError(cause);
				if (deploymentOutcomeUnknown(cause)) return { error: { message, field }, resourceId };
				if (previous === undefined && (await removed(resourceId))) {
					save(null);
					return {
						error: {
							message: `Couldn't deploy ${form.image.trim()}: ${message}`,
							field,
						},
						resourceId: undefined,
					};
				}
				return { error: { message, field }, resourceId };
			}
			if (form.isPublic) {
				const wantedDomain = `${form.subdomain}.${form.appDomain}`;
				const primary = currentDomains.find((domain) => domain.isPrimary);
				if (primary !== undefined && primary.domain !== wantedDomain) {
					await domains.updateResourceDomain({
						domainId: primary.id,
						domain: wantedDomain,
						updateMask: { paths: ["domain"] },
					});
				} else if (primary === undefined) {
					await domains.createResourceDomain({
						resourceId,
						environmentId: env.id,
						domain: {
							domainSource: DomainType.APP,
							subdomain: form.subdomain,
							appDomain: form.appDomain,
						},
					});
				}
			}
			return { error: null, resourceId };
		} catch (err) {
			const error = {
				message: getErrorMessage(err, "Couldn't finish the deployment"),
				field: apiFieldError(err),
			};
			return { error, resourceId };
		}
	};

	const deploy = async (form: NewResourceForm, defaults: DefaultServiceConfig) => {
		setDeployError(null);
		setIsPending(true);
		const result = await attempt(form, defaults);
		setIsPending(false);
		setDeployError(result.error);
		if (result.resourceId !== undefined) {
			void queryClient.invalidateQueries({
				queryKey: createConnectQueryKey({
					schema: getResource,
					input: { key: { case: "resourceId", value: result.resourceId } },
					transport,
					cardinality: "finite",
				}),
			});
		}
		void invalidateDeploys(queryClient, transport, workspaceId, result.resourceId);
		void queryClient.invalidateQueries({ queryKey: [DOMAIN_AVAILABILITY_KEY] });
		return result;
	};

	return {
		isPending,
		resourceId: pendingResource?.request.resourceId,
		pendingResourceId: pendingResource?.request.resourceId,
		pendingDomains: owned.data?.resource?.domains.filter((d) => d.environmentId === env.id).map((d) => d.domain) ?? [],
		deployError,
		deploy,
		clearError: () => {
			setDeployError(null);
		},
	};
}
