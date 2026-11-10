import { useMutation, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { deleteResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { Dialog } from "@/components/design/Dialog";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, Section } from "@/components/design/Page";
import { SoonTag } from "@/components/design/SoonTag";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useNow } from "@/hooks/useNow";
import { runtimeEventsOptions } from "@/hooks/runtimeEvents";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { resourceMissing } from "@/lib/not-found";
import { environmentStatus } from "@/lib/resource-status";
import { buildLogsPath, observabilityPath, resourcePath, workspacePath } from "@/lib/routes";
import { runtimeEvent } from "@/lib/runtime-events";
import { tsMs } from "@/lib/time";
import { NotFound } from "@/pages/NotFound";
import { NoticeBanner } from "@/pages/resource/Banners";
import { DeploymentsSection } from "@/pages/resource/DeploymentsSection";
import { DeleteDialogContent, RollbackDialogContent } from "@/pages/resource/Dialogs";
import { newestFirst, recentWarningCount } from "@/pages/resource/events";
import { EventsDrawer, type EventsTab } from "@/pages/resource/EventsDrawer";
import { shortId } from "@/pages/resource/format";
import { IncidentCard } from "@/pages/resource/IncidentCard";
import type { MetricRange } from "@/pages/resource/metric";
import {
	activeDeploymentPlaces,
	buildRegions,
	depImage,
	depTag,
	isDeployed,
	lastGoodDeployment,
	regionProblem,
	useResourceData,
	type ModalState,
	type Notice,
} from "@/pages/resource/model";
import { ReplicasPanel } from "@/pages/resource/ReplicasPanel";
import { ResourceHeader } from "@/pages/resource/ResourceHeader";
import { ResourceSkeleton } from "@/pages/resource/ResourceSkeleton";
import { RolloutProgress } from "@/pages/resource/RolloutProgress";
import { baseline, changeCount, type SettingsDraft } from "@/pages/resource/settings-draft";
import { SettingsView, type SettingsFocus } from "@/pages/resource/SettingsView";
import { SpecDialogContent } from "@/pages/resource/SpecDialog";
import { isInFlight } from "@/lib/deployment-utils";

const READ_LEVEL = 1;
const WRITE_LEVEL = 2;
const EVENTS_REFRESH_MS = 30_000;
const CLOCK_TICK_MS = 60_000;

type View = "overview" | SettingsFocus;

function parseView(value: string | null): View {
	if (value === "settings") return "settings";
	if (value === "variables") return "settings";
	if (value === "scaling") return "scaling";
	return "overview";
}

export function Resource() {
	const { resourceId = "" } = useParams<{ resourceId: string }>();
	const { active } = useEnvironments();
	return <ResourcePage key={`${resourceId}:${active?.id ?? ""}`} resourceId={resourceId} />;
}

function ResourcePage({ resourceId }: { resourceId: string }) {
	const { orgId = "" } = useParams<{ orgId: string }>();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const navigate = useNavigate();
	const transport = useTransport();
	const [params, setParams] = useSearchParams();
	const {
		resource,
		environmentId,
		deployments,
		currentDeployments,
		hasMoreHistory,
		isLoadingMoreHistory,
		historyError,
		loadMoreHistory,
		isLoading,
		error,
		refresh,
	} = useResourceData(resourceId);
	const { resourceLevel, workspaceLevel } = useMyScopes();
	const nowMs = useNow(CLOCK_TICK_MS).getTime();
	const configQuery = useQuery(getConfig, {});
	const eventsQuery = useTanstackQuery({
		...runtimeEventsOptions(transport, {
			orgId: activeOrgId ?? "",
			workspaceId: activeWorkspaceId ?? "",
			resourceId,
			environmentId: environmentId ?? "",
		}),
		refetchInterval: EVENTS_REFRESH_MS,
	});

	const canListResources =
		activeOrgId !== null && activeWorkspaceId !== null && workspaceLevel(activeOrgId, activeWorkspaceId) >= READ_LEVEL;
	const resourcesQuery = useTanstackQuery({
		...workspaceResourcesOptions(transport, activeWorkspaceId ?? ""),
		enabled: canListResources,
	});

	const [selectedRegion, setSelectedRegion] = useState<string | null>(null);
	const [range, setRange] = useState<MetricRange>("1h");
	const [modal, setModal] = useState<ModalState | null>(null);
	const [notice, setNotice] = useState<Notice | null>(null);
	const [redeploying, setRedeploying] = useState(false);
	const [eventsOpen, setEventsOpen] = useState(false);
	const [eventsTab, setEventsTab] = useState<EventsTab>("all");
	const [draft, setDraft] = useState<SettingsDraft>({});
	const deploy = useMutation(createDeployment);
	const del = useMutation(deleteResource);

	const view = parseView(params.get("tab"));
	const setView = (next: View) => {
		const p = new URLSearchParams(params);
		if (next === "overview") p.delete("tab");
		else p.set("tab", next);
		setParams(p, { replace: true });
	};

	if (isLoading) return <ResourceSkeleton />;

	if (error !== null) {
		if (resourceMissing(error, canListResources)) return <NotFound />;
		return (
			<Page>
				<EmptyState title="Couldn't load resource">{getErrorMessage(error, "Failed to load resource")}</EmptyState>
			</Page>
		);
	}
	if (resource === undefined) return <NotFound />;

	const environment = environmentStatus(resource, environmentId);
	const regions = buildRegions(resource, deployments, environment, currentDeployments);
	const deployed = regions.filter(isDeployed);
	const currents = deployed.flatMap((r) => (r.current?.spec === undefined ? [] : [r.current]));
	const events = newestFirst((eventsQuery.data?.events ?? []).map(runtimeEvent));
	const base = baseline(
		resource.name,
		regions,
		resource.domains.length > 0,
		configQuery.data?.serviceDefaults?.healthCheck,
	);
	const dirty = changeCount(draft, base) > 0;
	const canWrite =
		activeOrgId !== null &&
		activeWorkspaceId !== null &&
		resourceLevel(activeOrgId, activeWorkspaceId, resourceId) >= WRITE_LEVEL;

	const env = params.get("env");
	const envParam: Record<string, string> = env !== null ? { env } : {};
	const hasWs = activeOrgId !== null && activeWorkspaceId !== null;
	const obsLink = (obsView: "logs" | "metrics" | "events"): string | undefined =>
		hasWs ? observabilityPath(activeOrgId, activeWorkspaceId, obsView, { ...envParam, r: resource.name }) : undefined;
	const switcher = hasWs
		? (resourcesQuery.data?.resources ?? []).map((r) => ({
				id: r.id,
				name: r.name,
				status: environmentStatus(r, environmentId)?.status,
				href: `${resourcePath(activeOrgId, activeWorkspaceId, r.id)}${env !== null ? `?env=${encodeURIComponent(env)}` : ""}`,
			}))
		: [];

	const problemRegion = regions.find(
		(r) => (r.current === undefined || !isInFlight(r.current.status)) && regionProblem(r) !== undefined,
	);
	const problem = problemRegion === undefined ? undefined : regionProblem(problemRegion);
	const focusRegion = problemRegion ?? deployed[0];
	const rollbackTarget = focusRegion === undefined ? undefined : lastGoodDeployment(focusRegion);
	const markers = [...new Set(regions.flatMap((r) => r.history).map((d) => tsMs(d.createdAt)))];
	const specDeployments = [
		...new Map(
			[...currents, ...regions.flatMap((r) => r.history)]
				.toSorted((a, b) => tsMs(b.createdAt) - tsMs(a.createdAt))
				.map((d) => [d.id, d]),
		).values(),
	];

	const redeployFrom = async (deps: Deployment[]) => {
		const failures: string[] = [];
		for (const d of deps) {
			if (d.spec === undefined) continue;
			try {
				await deploy.mutateAsync({ resourceId, region: d.region, spec: d.spec, environmentId: d.environmentId });
			} catch (err) {
				failures.push(`${d.region}: ${getErrorMessage(err, "Failed to create deployment")}`);
			}
		}
		refresh();
		return failures;
	};

	const handleRedeploy = async () => {
		setRedeploying(true);
		const failures = await redeployFrom(currents);
		setRedeploying(false);
		if (failures.length > 0) {
			setNotice({ tone: "bad", title: `Redeploying ${resource.name} failed`, message: failures.join(" · ") });
			return;
		}
		const tags = [...new Set(currents.map((d) => depTag(d)))].join(", ");
		setNotice({
			tone: "info",
			title: `Redeploying ${resource.name}`,
			message: `New deployment from ${tags} in ${currents.map((d) => d.region).join(", ")}.`,
		});
	};

	const handleRollback = async (target: Deployment) => {
		const failures = await redeployFrom([target]);
		setModal(null);
		if (failures.length > 0) {
			setNotice({ tone: "bad", title: `Rolling back ${resource.name} failed`, message: failures.join(" · ") });
			return;
		}
		setNotice({
			tone: "info",
			title: `Rolling back ${resource.name} in ${target.region}`,
			message: `New deployment created from ${shortId(target.id)} (${depTag(target)}). Replicas are being replaced.`,
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
						const home = workspacePath(activeOrgId, activeWorkspaceId);
						void navigate(env !== null ? `${home}?env=${encodeURIComponent(env)}` : home);
					}
				},
				onError: (err) => {
					toastConnectError(err, "Failed to delete resource");
				},
			},
		);
	};

	const openRollback = (target: Deployment, current: Deployment | undefined) => {
		setModal({ kind: "rollback", target, current });
	};
	const openSpec = (dep: Deployment | undefined) => {
		if (dep !== undefined) setModal({ kind: "spec", deploymentId: dep.id, compareId: null });
	};

	return (
		<Page className="gap-5">
			<ResourceHeader
				nowMs={nowMs}
				resource={resource}
				environment={environment}
				regions={regions}
				switcher={switcher}
				warnings={recentWarningCount(events, nowMs)}
				isSettings={view !== "overview"}
				dirty={dirty}
				redeploying={redeploying}
				rollbackTarget={rollbackTarget}
				onEvents={() => {
					setEventsOpen(true);
				}}
				onSettings={() => {
					setView(view === "overview" ? "settings" : "overview");
				}}
				onRedeploy={() => void handleRedeploy()}
				onRollback={() => {
					if (rollbackTarget !== undefined) openRollback(rollbackTarget, focusRegion?.current);
				}}
				onScale={() => {
					setView("scaling");
				}}
				onViewSpec={() => {
					openSpec(deployed[0]?.current);
				}}
				onCopyImage={() => {
					void navigator.clipboard.writeText(depImage(deployed[0]?.current)).then(() => {
						toast.success("Copied image reference");
					});
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

			{view === "overview" ? (
				<div className="flex flex-col gap-5">
					{problem !== undefined && problemRegion !== undefined && environmentId !== undefined && (
						<IncidentCard
							nowMs={nowMs}
							problem={problem}
							region={problemRegion}
							lastGood={lastGoodDeployment(problemRegion)}
							events={events}
							workspaceId={resource.workspaceId}
							environmentId={environmentId}
							resourceId={resource.id}
							logsHref={obsLink("logs")}
							onRollback={openRollback}
						/>
					)}
					<RolloutProgress regions={regions} />
					{deployed.length > 0 && environmentId !== undefined ? (
						<ReplicasPanel
							nowMs={nowMs}
							workspaceId={resource.workspaceId}
							environmentId={environmentId}
							resourceId={resource.id}
							environment={environment}
							regions={regions}
							selected={selectedRegion}
							onSelect={setSelectedRegion}
							range={range}
							onRange={setRange}
							markers={markers}
							metricsHref={obsLink("metrics")}
						/>
					) : (
						<Section>
							<EmptyState title="Not deployed yet">
								Run <code className="font-mono">loco deploy</code> to create the first deployment of {resource.name}.
							</EmptyState>
						</Section>
					)}
					<Section
						title={
							<>
								Traffic
								<SoonTag />
							</>
						}
					>
						<p className="m-0 px-5 py-4 text-fg3">
							Requests, errors and latency for this resource are not collected yet.
						</p>
					</Section>
					<DeploymentsSection
						nowMs={nowMs}
						resourceId={resource.id}
						workspaceId={resource.workspaceId}
						regions={regions}
						hasMore={hasMoreHistory}
						isLoadingMore={isLoadingMoreHistory}
						historyError={historyError}
						onLoadMore={loadMoreHistory}
						logsHref={obsLink("logs")}
						buildLogsHref={(buildId) => (hasWs ? buildLogsPath(activeOrgId, activeWorkspaceId, buildId) : undefined)}
						canWrite={canWrite}
						canReadWorkspace={canListResources}
						onRollback={openRollback}
						onViewSpec={openSpec}
					/>
				</div>
			) : (
				<SettingsView
					resource={resource}
					environmentId={environmentId ?? ""}
					regions={regions}
					draft={draft}
					base={base}
					focus={view}
					canWrite={canWrite}
					onDraft={setDraft}
					onBack={() => {
						setView("overview");
					}}
					onDelete={() => {
						setModal({ kind: "delete" });
					}}
					onSaved={(next) => {
						setNotice(next);
						refresh();
					}}
				/>
			)}

			<EventsDrawer
				nowMs={nowMs}
				open={eventsOpen}
				onOpenChange={setEventsOpen}
				tab={eventsTab}
				onTab={setEventsTab}
				resourceId={resource.id}
				resourceName={resource.name}
				orgId={orgId}
				workspaceId={resource.workspaceId}
				events={events}
				isLoading={eventsQuery.isLoading}
				error={eventsQuery.error}
				eventsHref={obsLink("events")}
			/>

			<Dialog
				open={modal !== null}
				onOpenChange={(open: boolean) => {
					if (!open) setModal(null);
				}}
			>
				{modal?.kind === "spec" && (
					<SpecDialogContent
						deployments={specDeployments}
						deploymentId={modal.deploymentId}
						compareId={modal.compareId}
						onChange={(deploymentId, compareId) => {
							setModal({ kind: "spec", deploymentId, compareId });
						}}
					/>
				)}
				{modal?.kind === "rollback" && (
					<RollbackDialogContent
						resourceName={resource.name}
						target={modal.target}
						current={modal.current}
						pending={deploy.isPending}
						onConfirm={() => void handleRollback(modal.target)}
					/>
				)}
				{modal?.kind === "delete" && (
					<DeleteDialogContent
						resource={resource}
						places={activeDeploymentPlaces(resource)}
						pending={del.isPending}
						onConfirm={handleDelete}
					/>
				)}
			</Dialog>
		</Page>
	);
}
