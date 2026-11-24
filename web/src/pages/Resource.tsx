import { formatShortId } from "@/lib/utils";
import { useMutation, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { BellIcon, FileCodeIcon, RotateCwIcon, ScrollTextIcon, Settings2Icon, Undo2Icon } from "lucide-react";
import { useEffect, useState } from "react";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "@/lib/toast";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentTrigger, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { deleteResource, getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { DeleteResourceDialog } from "@/components/DeleteResourceDialog";
import { InlineCode } from "@/components/design/InlineCode";
import { PageCommands, type PageCommand } from "@/components/shell/CommandMenu";
import { Dialog } from "@/components/design/Dialog";
import { EmptyState } from "@/components/design/EmptyState";
import { ErrorState } from "@/components/design/ErrorState";
import { Page, Section } from "@/components/design/Page";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { useNow } from "@/hooks/useNow";
import { runtimeEventsOptions } from "@/hooks/runtimeEvents";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { deploymentRegions } from "@/lib/deployment-utils";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { notFound, resourceMissing } from "@/lib/not-found";
import { configuredRegions } from "@/lib/resource-regions";
import { environmentStatus } from "@/lib/resource-status";
import { activitySearch, buildLogsPath, observabilityPath, resourcePath, resourcesPath, withEnv } from "@/lib/routes";
import { runtimeEvent } from "@/lib/runtime-events";
import { tsMs } from "@/lib/time";
import { NoticeBanner, SecretsChangedNotice } from "@/pages/resource/Banners";
import { DeploymentsSection } from "@/pages/resource/DeploymentsSection";
import { createDeployments, currentSources, toastRedeploying, type DeploymentSource } from "@/pages/resource/redeploy";
import { RedeployDialogContent, RollbackDialogContent } from "@/pages/resource/Dialogs";
import { newestFirst, recentWarningCount } from "@/pages/resource/events";
import { IncidentCard } from "@/pages/resource/IncidentCard";
import { METRIC_RANGES } from "@/pages/resource/metric";
import {
	buildRegions,
	changedSecrets,
	canRollBackTo,
	depTag,
	focusRegion,
	isDeployed,
	metricsPending,
	problemRegionOf,
	regionProblem,
	useResourceData,
	type ModalState,
	type Notice,
} from "@/pages/resource/model";
import { MetricsPanel } from "@/pages/resource/MetricsPanel";
import { ResourceHeader } from "@/pages/resource/ResourceHeader";
import { ResourceSkeleton } from "@/pages/resource/ResourceSkeleton";
import { DeploymentProgress } from "@/pages/resource/DeploymentProgress";
import { queryStr, quoteValue } from "@/pages/observability/query";
import { LeaveGuard } from "@/pages/resource/LeaveGuard";
import { baseline, changeCount, type SettingsDraft } from "@/pages/resource/settings-draft";
import { SETTINGS_FOCUSES, SettingsView } from "@/pages/resource/SettingsView";
import { SpecDialogContent } from "@/pages/resource/SpecDialog";

const EVENTS_REFRESH_MS = 30_000;
const CLOCK_TICK_MS = 60_000;

const VIEWS = ["overview", ...SETTINGS_FOCUSES] as const;
type View = (typeof VIEWS)[number];

const RESOURCE_PARAMS = {
	tab: parseAsStringLiteral(VIEWS).withDefault("overview"),
	env: parseAsString,
	range: parseAsStringLiteral(METRIC_RANGES).withDefault("1h"),
};

export function Resource() {
	const { resourceId = "" } = useParams<{ resourceId: string }>();
	const { active } = useEnvironments();
	// The page starts over when the user switches environment, not when the first one loads.
	const [shown, setShown] = useState({ environmentId: active?.id, switches: 0 });
	if (active !== undefined && active.id !== shown.environmentId) {
		setShown({
			environmentId: active.id,
			switches: shown.environmentId === undefined ? shown.switches : shown.switches + 1,
		});
	}
	return <ResourcePage key={`${resourceId}:${shown.switches}`} resourceId={resourceId} />;
}

function ResourcePage({ resourceId }: { resourceId: string }) {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const navigate = useNavigate();
	const transport = useTransport();
	const [{ tab: view, env, range }, setParams] = useQueryStates(RESOURCE_PARAMS, {
		history: "replace",
	});
	const {
		resource: loaded,
		environmentId,
		deployments,
		currentDeployments,
		isLoading,
		error,
		refresh,
	} = useResourceData(resourceId);
	const { resourceLevel, workspaceLevel } = useMyScopes();
	const nowMs = useNow(CLOCK_TICK_MS).getTime();
	const configQuery = useQuery(getConfig, {});
	const eventScope = {
		orgId: activeOrgId ?? "",
		workspaceId: activeWorkspaceId ?? "",
		resourceId,
		environmentId: environmentId ?? "",
	};
	const eventsQuery = useTanstackQuery({
		...runtimeEventsOptions(transport, eventScope),
		refetchInterval: EVENTS_REFRESH_MS,
	});

	const canListResources =
		activeOrgId !== null &&
		activeWorkspaceId !== null &&
		workspaceLevel(activeOrgId, activeWorkspaceId) >= scopeLevel(Scope.READ);
	const resourcesQuery = useTanstackQuery({
		...workspaceResourcesOptions(transport, activeWorkspaceId ?? "", "workspace"),
		enabled: canListResources,
		// The shell's activity drawer already observes this list from mount and keeps it current.
		refetchOnMount: false,
	});

	const secretsQuery = useQuery(
		listSecrets,
		{ environmentId: environmentId ?? "" },
		{ enabled: environmentId !== undefined && canListResources },
	);

	const [modal, setModal] = useState<ModalState | null>(null);
	const [notice, setNotice] = useState<Notice | null>(null);
	const [redeploying, setRedeploying] = useState(false);
	const [draft, setDraft] = useState<SettingsDraft>({});
	const deploy = useMutation(createDeployment);
	const del = useMutation(deleteResource);

	const [search] = useSearchParams();
	const rawTab = search.get("tab");
	const rawRange = search.get("range");
	const badTab = rawTab !== null && !(VIEWS as readonly string[]).includes(rawTab);
	const badRange = rawRange !== null && !(METRIC_RANGES as readonly string[]).includes(rawRange);
	useEffect(() => {
		if (badTab || badRange)
			void setParams({ ...(badTab && { tab: "overview" }), ...(badRange && { range: "1h" }) } as const, {
				history: "replace",
			});
	}, [badTab, badRange, setParams]);

	const setView = (next: View) => {
		void setParams({ tab: next }, { history: "push" });
	};

	if (isLoading || configQuery.isPending) return <ResourceSkeleton />;
	if (configQuery.error !== null)
		return (
			<Page>
				<EmptyState title="Couldn't load service defaults">
					{getErrorMessage(configQuery.error, "Failed to load service defaults")}
				</EmptyState>
			</Page>
		);
	const defaults = configQuery.data.serviceDefaults;
	if (defaults === undefined) return <ResourceSkeleton />;

	if (error !== null && resourceMissing(error, canListResources)) notFound();
	// A failed load keeps the header from the workspace's resource list when it has this resource.
	const resource =
		loaded ?? (error === null ? undefined : resourcesQuery.data?.resources.find((r) => r.id === resourceId));
	if (resource === undefined) {
		if (error === null) notFound();
		return (
			<Page>
				<ErrorState error={error} what="this resource" rpc={getResource} onRetry={refresh} />
			</Page>
		);
	}

	const environment = environmentStatus(resource, environmentId);
	const regions = buildRegions(resource, deployments, environment, currentDeployments);
	const deployed = regions.filter(isDeployed);
	const noMetricsYet = metricsPending(deployed, nowMs);
	const redeploySources = currentSources(regions);
	const currents = redeploySources.map((s) => s.deployment);
	const events = newestFirst((eventsQuery.data?.events ?? []).map(runtimeEvent));
	const base = baseline(
		resource.slug,
		resource.name,
		resource.description ?? "",
		regions,
		configuredRegions(resource),
		resource.domains.length > 0,
		defaults,
		resource.failurePolicy,
	);
	const level =
		activeOrgId !== null && activeWorkspaceId !== null ? resourceLevel(activeOrgId, activeWorkspaceId, resourceId) : 0;
	const canWrite = level >= scopeLevel(Scope.WRITE);
	const canDelete = level >= scopeLevel(Scope.ADMIN);
	const staleSecrets = changedSecrets(secretsQuery.data?.secrets ?? [], resourceId, regions);
	const secretsNotice = (id: string) => (
		<SecretsChangedNotice
			id={id}
			names={staleSecrets}
			canWrite={canWrite}
			onRestart={() => {
				setModal({ kind: "redeploy" });
			}}
		/>
	);

	const envParam: Record<string, string> = env !== null ? { env } : {};
	const hasWs = activeOrgId !== null && activeWorkspaceId !== null;
	const buildLogsHref = (buildId: string) =>
		hasWs ? buildLogsPath(activeOrgId, activeWorkspaceId, buildId) : undefined;
	const obsLink = (
		obsView: "logs" | "metrics",
		filter: { key: string; value: string } | null = null,
	): string | undefined =>
		hasWs
			? observabilityPath(activeOrgId, activeWorkspaceId, obsView, {
					...envParam,
					r: [resource.slug],
					q: filter === null ? null : queryStr([{ key: filter.key, value: quoteValue(filter.value), neg: false }], ""),
				})
			: undefined;
	const switcher = hasWs
		? (resourcesQuery.data?.resources ?? []).map((r) => ({
				id: r.id,
				name: r.name,
				description: r.description ?? "",
				status: environmentStatus(r, environmentId)?.status,
				href: resourcePath(activeOrgId, activeWorkspaceId, r.id, { env }),
			}))
		: [];

	const problemRegion = problemRegionOf(regions);
	const problem = problemRegion === undefined ? undefined : regionProblem(problemRegion);
	const focus = focusRegion(regions);
	const rollbackTarget = focus?.lastGood;
	const specDeployments = [
		...new Map(
			[...(modal?.kind === "spec" ? modal.shown : []), ...currents, ...regions.flatMap((r) => r.history)]
				.toSorted((a, b) => tsMs(b.createdAt) - tsMs(a.createdAt))
				.map((d) => [d.id, d]),
		).values(),
	];

	/** The deployments that run in the same regions as `target` and can be rolled back to. */
	const rollbackOptions = (target: Deployment) => {
		const names = deploymentRegions(target);
		const targetRegions = regions.filter((r) => names.includes(r.name));
		return specDeployments.filter(
			(d) => deploymentRegions(d).join() === names.join() && targetRegions.every((r) => canRollBackTo(d, r)),
		);
	};

	const redeployFrom = async (sources: DeploymentSource[], trigger: DeploymentTrigger) => {
		const failures = await createDeployments(deploy.mutateAsync, resourceId, sources, trigger);
		void refresh();
		return failures;
	};

	const handleRedeploy = async () => {
		setRedeploying(true);
		const failures = await redeployFrom(redeploySources, DeploymentTrigger.REDEPLOY);
		setRedeploying(false);
		setModal(null);
		if (failures.length > 0) {
			setNotice({
				tone: "bad",
				title: `Redeploying ${resource.name} failed`,
				message: failures.join("\n"),
			});
			return;
		}
		toastRedeploying(resource.name, redeploySources);
	};

	const handleRollback = async (target: Deployment) => {
		if (!canWrite) return;
		const failures = await redeployFrom(
			[{ deployment: target, regions: deploymentRegions(target) }],
			DeploymentTrigger.ROLLBACK,
		);
		setModal(null);
		if (failures.length > 0) {
			setNotice({
				tone: "bad",
				title: `Rolling back ${resource.name} failed`,
				message: failures.join("\n"),
			});
			return;
		}
		toast.info(`Rolling back ${resource.name} in ${deploymentRegions(target).join(", ")}`, {
			description: `New deployment created from ${formatShortId(target.id)} (${depTag(target)}). Replicas are being replaced.`,
		});
	};

	const handleDelete = () => {
		del.mutate(
			{ resourceId },
			{
				onSuccess: () => {
					toast.success(`Deleted ${resource.name}`);
					setModal(null);
					if (hasWs) {
						void navigate(withEnv(resourcesPath(activeOrgId, activeWorkspaceId), env));
					}
				},
				onError: (err) => {
					toastConnectError(err, "Failed to delete resource");
				},
			},
		);
	};

	const openRollback = (target: Deployment, current: Deployment | undefined) => {
		if (!canWrite) return;
		setModal({ kind: "rollback", target, current });
	};
	const openSpec = (dep: Deployment | undefined, shown: Deployment[] = []) => {
		if (dep !== undefined) setModal({ kind: "spec", deploymentId: dep.id, compareId: null, shown: [dep, ...shown] });
	};
	const compareSpecs = (from: Deployment, to: Deployment) => {
		setModal({ kind: "spec", deploymentId: from.id, compareId: to.id, shown: [from, to] });
	};
	const openEvents = () => {
		void navigate({ search: activitySearch(search, resource.slug) });
	};
	const logsHref = obsLink("logs");
	const spec = deployed[0]?.current;
	const canRedeploy = canWrite && deployed.length > 0 && !redeploying;
	const askRedeploy = () => {
		setModal({ kind: "redeploy" });
	};
	const askRollback = () => {
		if (rollbackTarget !== undefined) openRollback(rollbackTarget, focus?.current);
	};
	const pageCommands: PageCommand[] = [];
	if (canRedeploy)
		pageCommands.push({
			id: "redeploy",
			label: `Restart ${resource.name}`,
			icon: <RotateCwIcon />,
			run: askRedeploy,
		});
	if (canWrite && rollbackTarget !== undefined)
		pageCommands.push({
			id: "rollback",
			label: `Roll back ${resource.name}`,
			icon: <Undo2Icon />,
			run: askRollback,
		});
	if (spec !== undefined)
		pageCommands.push({
			id: "view-spec",
			label: "View spec",
			meta: resource.name,
			icon: <FileCodeIcon />,
			run: () => {
				openSpec(spec);
			},
		});
	pageCommands.push(
		{
			id: "events",
			label: "Open events",
			keywords: "warnings activity",
			meta: resource.name,
			icon: <BellIcon />,
			run: openEvents,
		},
		{
			id: "settings",
			label: "Settings",
			keywords: "variables env scaling",
			meta: resource.name,
			icon: <Settings2Icon />,
			run: () => {
				setView("settings");
			},
		},
	);
	if (logsHref !== undefined)
		pageCommands.push({
			id: "logs",
			label: `Logs for ${resource.name}`,
			icon: <ScrollTextIcon />,
			run: () => {
				void navigate(logsHref);
			},
		});

	const incident =
		problem !== undefined && problemRegion !== undefined && environmentId !== undefined ? (
			<IncidentCard
				nowMs={nowMs}
				problem={problem}
				region={problemRegion}
				lastGood={problemRegion.lastGood}
				workspaceId={resource.workspaceId}
				environmentId={environmentId}
				resourceId={resource.id}
				logsHref={(replica) => obsLink("logs", replica === null ? null : { key: "replica", value: replica })}
				canWrite={canWrite}
				onRollback={openRollback}
			/>
		) : undefined;

	return (
		<Page className="gap-5">
			<PageCommands items={pageCommands} />
			<LeaveGuard dirty={!del.isPending && changeCount(draft, base) > 0} resourceName={resource.name} />
			<ResourceHeader
				resource={resource}
				environment={environment}
				regions={regions}
				switcher={switcher}
				warnings={recentWarningCount(events, nowMs)}
				isSettings={view !== "overview"}
				canRedeploy={canRedeploy}
				canWrite={canWrite}
				canDelete={canDelete}
				rollbackTarget={rollbackTarget}
				onEvents={openEvents}
				onSettings={() => {
					setView(view === "overview" ? "settings" : "overview");
				}}
				onRedeploy={askRedeploy}
				onRollback={askRollback}
				onScale={() => {
					setView("scaling");
				}}
				onViewSpec={() => {
					openSpec(deployed[0]?.current);
				}}
				onDelete={() => {
					setModal({ kind: "delete" });
				}}
			/>
			{notice !== null && (
				<NoticeBanner
					notice={notice}
					onDismiss={() => {
						setNotice(null);
					}}
				/>
			)}

			{error !== null ? (
				<ErrorState error={error} what="this resource" rpc={getResource} onRetry={refresh} />
			) : view === "overview" ? (
				<div className="flex flex-col gap-5">
					{secretsNotice("secrets-notice-ov")}
					<DeploymentProgress
						workspaceId={resource.workspaceId}
						fallback={incident}
						deployTimeoutSeconds={resource.failurePolicy?.deployTimeoutSeconds}
						regions={regions}
						logsHref={obsLink("logs")}
						regionLogsHref={(region) => obsLink("logs", { key: "region", value: region })}
						buildLogsHref={buildLogsHref}
						canWrite={canWrite}
						onCompare={compareSpecs}
					/>
					{deployed.length > 0 && environmentId !== undefined ? (
						<MetricsPanel
							nowMs={nowMs}
							pendingName={noMetricsYet ? resource.name : undefined}
							workspaceId={resource.workspaceId}
							environmentId={environmentId}
							resourceId={resource.id}
							regions={regions}
							range={range}
							onRange={(next) => {
								void setParams({ range: next });
							}}
							replicaLogsHref={(replica) => obsLink("logs", { key: "replica", value: replica })}
						/>
					) : (
						<Section>
							<EmptyState title="Not deployed yet">
								Run <InlineCode>loco deploy</InlineCode> to create the first deployment of {resource.name}.
							</EmptyState>
						</Section>
					)}
					<DeploymentsSection
						resourceName={resource.name}
						environmentId={environmentId}
						nowMs={nowMs}
						resourceId={resource.id}
						workspaceId={resource.workspaceId}
						regions={regions}
						buildLogsHref={buildLogsHref}
						canWrite={canWrite}
						canReadWorkspace={canListResources}
						onRollback={openRollback}
						onViewSpec={openSpec}
						onCompareSpec={compareSpecs}
					/>
				</div>
			) : (
				<div className="flex flex-col gap-5">
					<SettingsView
						resource={resource}
						secretsNotice={secretsNotice("secrets-notice-set")}
						environmentId={environmentId ?? ""}
						regions={regions}
						draft={draft}
						base={base}
						focus={view}
						canWrite={canWrite}
						canDelete={canDelete}
						canReadWorkspace={canListResources}
						onDraft={setDraft}
						onBack={() => {
							setView("overview");
						}}
						onDelete={() => {
							setModal({ kind: "delete" });
						}}
						onSaved={(next) => {
							setNotice(next);
							void refresh();
						}}
					/>
				</div>
			)}

			<Dialog
				open={modal !== null && modal.kind !== "delete"}
				onOpenChange={(open: boolean) => {
					if (!open) setModal(null);
				}}
			>
				{modal?.kind === "spec" && (
					<SpecDialogContent
						resourceName={resource.name}
						deployments={specDeployments}
						deploymentId={modal.deploymentId}
						compareId={modal.compareId}
						onChange={(deploymentId, compareId) => {
							setModal({ ...modal, deploymentId, compareId });
						}}
					/>
				)}
				{modal?.kind === "rollback" && (
					<RollbackDialogContent
						resourceName={resource.name}
						target={modal.target}
						current={modal.current}
						options={rollbackOptions(modal.target)}
						onTarget={(target) => {
							setModal({ ...modal, target });
						}}
						pending={deploy.isPending}
						onConfirm={() => void handleRollback(modal.target)}
					/>
				)}
				{modal?.kind === "redeploy" && (
					<RedeployDialogContent
						resourceName={resource.name}
						running={currents}
						pending={redeploying}
						onConfirm={() => void handleRedeploy()}
					/>
				)}
			</Dialog>
			<DeleteResourceDialog
				open={modal?.kind === "delete"}
				onOpenChange={(open) => {
					if (!open) setModal(null);
				}}
				resource={resource}
				confirmLabel={`Delete ${resource.name}`}
				asQuestion={false}
				pending={del.isPending}
				onConfirm={handleDelete}
			/>
		</Page>
	);
}
