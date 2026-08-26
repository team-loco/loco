import { useAuth } from "@/auth/AuthProvider";
import {
	DeploymentWizard,
	type DeploymentWizardValues,
} from "@/components/DeploymentWizard";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { getDefaultServiceConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import { listEnvironments } from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { createResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { LoggingConfigSchema, MetricsConfigSchema, RegionTargetSchema, ResourceSpecSchema, ResourceType, RoutingConfigSchema, ServiceSpecSchema, TracingConfigSchema } from "@gen/loco/resource/v1/resource_pb";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { getErrorMessage } from "@/lib/error-handler";
import { create } from "@bufbuild/protobuf";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";

function generateAppName() {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789";
	let hash = "";
	for (let i = 0; i < 4; i++) {
		hash += chars[Math.floor(Math.random() * chars.length)];
	}
	return `myapp-${hash}`;
}

export function CreateResource({ onClose }: { onClose?: () => void } = {}) {
	const navigate = useNavigate();
	const { workspaceId: paramWorkspaceId } = useParams();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();

	const [open, setOpen] = useState(true);
	const [resourceName] = useState(generateAppName);

	// Queries
	const { user } = useAuth();
	const { data: orgsRes } = useQuery(
		listUserOrgs,
		{ userId: user?.id },
		{ enabled: !!user },
	);
	const orgs       = orgsRes?.orgs ?? [];
	const firstOrgId = orgs.length > 0 ? orgs[0].id : null;

	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId },
	);
	const workspaces  = workspacesRes?.workspaces ?? [];
	const workspaceId = paramWorkspaceId ?? (workspaces.length > 0 ? workspaces[0].id : null);

	const { data: platformDomainsRes } = useQuery(listPlatformDomains, { activeOnly: true });
	const { data: defaultConfigRes }   = useQuery(getDefaultServiceConfig, {});
	const { data: environmentsRes }    = useQuery(listEnvironments, { workspaceId: paramWorkspaceId });

	const platformDomains = useMemo(
		() => platformDomainsRes?.platformDomains ?? [],
		[platformDomainsRes?.platformDomains],
	);
	const environments = useMemo(
		() => environmentsRes?.environments ?? [],
		[environmentsRes?.environments],
	);

	const defaultDomain  = defaultConfigRes?.config?.platformDomain;
	const platformDomain = useMemo(
		() =>
			platformDomains.find((d) => d.domain === defaultDomain) ??
			platformDomains.at(0),
		[platformDomains, defaultDomain],
	);

	// Mutations
	const createResourceMutation   = useMutation(createResource);
	const createDeploymentMutation = useMutation(createDeployment);

	const isCreating =
		createResourceMutation.isPending || createDeploymentMutation.isPending;

	const handleClose = () => {
		setOpen(false);
		if (onClose) {
			onClose();
			return;
		}
		if (activeOrgId && activeWorkspaceId) {
			void navigate(`/org/${activeOrgId}/wks/${activeWorkspaceId}`);
		} else {
			void navigate(-1);
		}
	};

	const handleSubmit = async (values: DeploymentWizardValues) => {
		if (!workspaceId) {
			toast.error("No workspace available");
			return;
		}

		try {
			const routing = create(RoutingConfigSchema, {
				port:        values.port,
				pathPrefix:  "/",
				idleTimeout: 30,
			});

			const logging = create(LoggingConfigSchema, {
				enabled:         true,
				retentionPeriod: "7d",
				structured:      true,
			});

			const metrics = create(MetricsConfigSchema, {
				enabled: true,
				path:    "/metrics",
				port:    9090,
			});

			const tracing = create(TracingConfigSchema, {
				enabled:    false,
				sampleRate: 0.1,
				tags:       {},
			});

			const regionTarget = create(RegionTargetSchema, {
				enabled:     true,
				primary:     true,
				cpu:         values.cpu,
				memory:      values.memory,
				minReplicas: 1,
				maxReplicas: 1,
			});

			const serviceSpec = create(ServiceSpecSchema, {
				routing,
				observability: { logging, metrics, tracing },
				regions:       { [values.region]: regionTarget },
			});

			const spec = create(ResourceSpecSchema, {
				spec: { case: "service", value: serviceSpec },
			});

			const resource = await createResourceMutation.mutateAsync({
				name:        resourceName,
				workspaceId: workspaceId,
				type:        ResourceType.SERVICE,
				domain:      values.networkEnabled
					? {
							domainSource:     DomainType.PLATFORM_PROVIDED,
							subdomain:        values.subdomain,
							platformDomainId: platformDomain?.id,
						}
					: undefined,
				spec,
			});

			if (!resource.resourceId) {
				toast.error("Failed to create resource");
				return;
			}

			try {
				await createDeploymentMutation.mutateAsync({
					resourceId: resource.resourceId,
					region:     values.region,
					spec: {
						spec: {
							case:  "service",
							value: {
								build:       { type: "image", image: values.imageUrl },
								cpu:         values.cpu,
								memory:      values.memory,
								minReplicas: 1,
								maxReplicas: 1,
								port:        values.port,
								env:         values.envVars,
							},
						},
					},
					environmentId: environments[0]?.id,
				});
				toast.success("Resource created and deployment started");
			} catch (deployError) {
				toast.warning(
					`Resource created, but deployment failed: ${getErrorMessage(deployError, "Unknown error")}`,
				);
			}

			if (activeOrgId && activeWorkspaceId) {
				void navigate(
					`/org/${activeOrgId}/wks/${activeWorkspaceId}/resource/${resource.resourceId}`,
				);
			}
		} catch (error) {
			toast.error(getErrorMessage(error, "Failed to create resource"));
		}
	};

	return (
		<DeploymentWizard
			open={open}
			onClose={handleClose}
			title="Create Resource"
			submitLabel="Create & Deploy"
			showSubdomain
			platformDomain={platformDomain}
			initialSubdomain={resourceName}
			onSubmit={handleSubmit}
			isSubmitting={isCreating}
		/>
	);
}
