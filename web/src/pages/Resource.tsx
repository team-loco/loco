import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentPhase, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { deleteResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { Dialog } from "@/components/design/Dialog";
import { EmptyState } from "@/components/design/EmptyState";
import { Page } from "@/components/design/Page";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { observabilityPath, workspacePath } from "@/lib/routes";
import { ErrorBanner, NoticeBanner } from "@/pages/resource/Banners";
import { DeploymentsSection } from "@/pages/resource/DeploymentsSection";
import { DeleteDialogContent, RollbackDialogContent, SpecDialogContent } from "@/pages/resource/Dialogs";
import { DiffDialogContent } from "@/pages/resource/DiffDialog";
import { EventsSection } from "@/pages/resource/EventsSection";
import { shortId } from "@/pages/resource/format";
import { buildRegions, depService, depTag, useResourceData, type ModalState, type Notice } from "@/pages/resource/model";
import { RegionPanel } from "@/pages/resource/RegionPanel";
import { ResourceHeader } from "@/pages/resource/ResourceHeader";
import { ResourceSkeleton } from "@/pages/resource/ResourceSkeleton";
import { parseTab, ResourceToolbar, type ObsLinks, type ResourceTab } from "@/pages/resource/ResourceToolbar";
import { SettingsTab } from "@/pages/resource/SettingsTab";
import type { MetricRange } from "@/pages/resource/useRegionMetric";
import { VariablesTab } from "@/pages/resource/VariablesTab";

export function Resource() {
	const { resourceId = "" } = useParams<{ resourceId: string }>();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const navigate = useNavigate();
	const [params, setParams] = useSearchParams();
	const { resource, deployments, isLoading, error, refresh } = useResourceData(resourceId);

	const [regionName, setRegionName] = useState<string | null>(null);
	const [range, setRange] = useState<MetricRange>("1h");
	const [modal, setModal] = useState<ModalState | null>(null);
	const [notice, setNotice] = useState<Notice | null>(null);
	const [redeploying, setRedeploying] = useState(false);
	const deploy = useMutation(createDeployment);
	const del = useMutation(deleteResource);

	const tab = parseTab(params.get("tab"));
	const setTab = (next: ResourceTab) => {
		const p = new URLSearchParams(params);
		if (next === "overview") p.delete("tab");
		else p.set("tab", next);
		setParams(p, { replace: true });
	};

	if (isLoading) return <ResourceSkeleton />;

	if (error !== null || resource === undefined) {
		const message = error !== null ? getErrorMessage(error, "Failed to load resource") : "This resource does not exist or was deleted.";
		return (
			<Page>
				<EmptyState title={error !== null ? "Couldn't load resource" : "Resource not found"}>{message}</EmptyState>
			</Page>
		);
	}

	const regions = buildRegions(resource, deployments);
	const region = regions.find((r) => r.name === regionName) ?? regions[0];
	const regionNames = regions.map((r) => r.name);
	const primary = regions.find((r) => r.primary) ?? regions[0];
	const currents = regions.flatMap((r) => (r.current?.spec === undefined ? [] : [r.current]));
	const primaryDomain = resource.domains.find((d) => d.isPrimary) ?? resource.domains[0];

	const env = params.get("env");
	const hasWs = activeOrgId !== null && activeWorkspaceId !== null;
	const obsLink = (view: "logs" | "metrics" | "traces"): string =>
		hasWs ? observabilityPath(activeOrgId, activeWorkspaceId, view, { ...(env !== null ? { env } : {}), r: resource.name }) : "";
	const obs: ObsLinks | undefined = hasWs
		? { logs: obsLink("logs"), metrics: obsLink("metrics"), traces: obsLink("traces") }
		: undefined;

	const failing = regions.find((r) => (r.config?.lastError ?? "") !== "" || r.current?.status === DeploymentPhase.FAILED);
	const failTitle =
		failing === undefined
			? ""
			: (failing.config?.lastError ?? "") !== ""
				? `${failing.name} is failing`
				: `Deployment ${shortId(failing.current?.id ?? "")} failed in ${failing.name}`;
	const failMessage = failing === undefined ? "" : (failing.config?.lastError ?? "") !== "" ? (failing.config?.lastError ?? "") : (failing.current?.message ?? "");

	const scrollEvents = () => {
		document.getElementById("events")?.scrollIntoView({ behavior: "smooth", block: "start" });
	};

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
			message: `New deployment created from ${shortId(target.id)} (${depTag(target)}). Pods are being replaced.`,
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
						const base = workspacePath(activeOrgId, activeWorkspaceId);
						void navigate(env !== null ? `${base}?env=${encodeURIComponent(env)}` : base);
					}
				},
				onError: (err) => {
					toastConnectError(err, "Failed to delete resource");
				},
			},
		);
	};

	const specDep = primary?.current;
	const diffRegion = modal?.kind === "diff" ? regions.find((r) => r.name === modal.region) : undefined;

	return (
		<Page className="gap-5">
			<ResourceHeader
				resource={resource}
				regions={regions}
				hasDeployments={deployments.length > 0}
				onViewSpec={() => {
					setModal({ kind: "spec" });
				}}
			/>
			<ResourceToolbar
				tab={tab}
				onTab={setTab}
				domain={primaryDomain?.domain}
				obs={obs}
				canRedeploy={currents.length > 0}
				redeploying={redeploying}
				onRedeploy={() => void handleRedeploy()}
			/>
			{notice !== null && (
				<NoticeBanner
					notice={notice}
					onDismiss={() => {
						setNotice(null);
					}}
				/>
			)}

			{tab === "overview" && (
				<div className="flex flex-col gap-5">
					{failing !== undefined && (
						<ErrorBanner title={failTitle} message={failMessage} logsHref={obs?.logs} onEvents={scrollEvents} />
					)}
					{region !== undefined ? (
						<RegionPanel
							workspaceId={resource.workspaceId}
							resourceId={resourceId}
							regions={regions}
							region={region}
							onRegion={setRegionName}
							range={range}
							onRange={setRange}
						/>
					) : (
						<section className="rounded-lg border border-line">
							<EmptyState title="Not deployed yet">
								Run <code className="font-mono">loco deploy</code> to create the first deployment of {resource.name}.
							</EmptyState>
						</section>
					)}
					<DeploymentsSection
						regions={regions}
						onDiff={(r, fromId, toId) => {
							setModal({ kind: "diff", region: r, fromId, toId });
						}}
						onRollback={(target, current) => {
							setModal({ kind: "rollback", target, current });
						}}
					/>
					<EventsSection resourceId={resourceId} multiRegion={regions.length > 1} />
				</div>
			)}

			{tab === "variables" && (
				<VariablesTab
					resourceId={resourceId}
					resourceName={resource.name}
					env={depService(specDep)?.env ?? {}}
					regionNames={regionNames}
					hasDeployment={currents.length > 0}
					onNotice={setNotice}
					onSaved={refresh}
				/>
			)}

			{tab === "settings" && (
				<SettingsTab
					resource={resource}
					regions={regions}
					onNotice={setNotice}
					onSaved={refresh}
					onDelete={() => {
						setModal({ kind: "delete" });
					}}
				/>
			)}

			<Dialog
				open={modal !== null}
				onOpenChange={(open: boolean) => {
					if (!open) setModal(null);
				}}
			>
				{modal?.kind === "spec" && specDep !== undefined && (
					<SpecDialogContent dep={specDep} specVersion={resource.specVersion} />
				)}
				{modal?.kind === "diff" && diffRegion !== undefined && (
					<DiffDialogContent
						region={diffRegion}
						fromId={modal.fromId}
						toId={modal.toId}
						onChange={(fromId, toId) => {
							setModal({ kind: "diff", region: modal.region, fromId, toId });
						}}
						onRollback={(target, current) => {
							setModal({ kind: "rollback", target, current });
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
						regionNames={regions.filter((r) => r.current !== undefined).map((r) => r.name)}
						pending={del.isPending}
						onConfirm={handleDelete}
					/>
				)}
			</Dialog>
		</Page>
	);
}
