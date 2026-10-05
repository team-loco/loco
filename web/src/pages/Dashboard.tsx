import { BoxIcon, PlusIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { effectiveResourceStatus } from "@/components/design/StatusBadge";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useBreadcrumbs } from "@/context/ShellContext";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage } from "@/lib/error-handler";
import { resourcePath } from "@/lib/routes";
import { ArchitectureDiagram, type DiagramService } from "@/pages/dashboard/ArchitectureDiagram";
import { AttentionSection, buildAttention } from "@/pages/dashboard/AttentionSection";
import { CreateResourceMenu } from "@/pages/dashboard/CreateResourceMenu";
import { DraftDrawer } from "@/pages/dashboard/DraftDrawer";
import { DRAFT_DEFAULTS, nameFromImage, uniqueName, useDrafts } from "@/pages/dashboard/drafts";
import { EnvironmentMenu } from "@/pages/dashboard/EnvironmentMenu";
import { NewServiceDialog } from "@/pages/dashboard/NewServiceDialog";
import { RecentDeployments } from "@/pages/dashboard/RecentDeployments";
import { ResourcesTable } from "@/pages/dashboard/ResourcesTable";
import { useDashboardData } from "@/pages/dashboard/useDashboardData";

function DashboardSkeleton() {
	return (
		<>
			<Section title="Architecture">
				<div className="flex flex-col gap-3.5 p-6">
					<Skeleton className="h-[60px] w-[300px] self-end" />
					<Skeleton className="h-[60px] w-[180px] self-center" />
					<Skeleton className="h-[60px] w-[300px] self-end" />
				</div>
			</Section>
			<Section title="Resources">
				<div className="flex flex-col gap-2 p-4">
					{Array.from({ length: 4 }, (_, i) => (
						<Skeleton key={i} className="h-9 w-full" />
					))}
				</div>
			</Section>
		</>
	);
}

export function Dashboard() {
	useBreadcrumbs("Overview");
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { environments, active, setActive, isLoading: envsLoading } = useEnvironments();
	const data = useDashboardData(activeWorkspaceId, active);
	const drafts = useDrafts(activeWorkspaceId, active?.id);
	const now = useNow(60_000);
	const nowMs = now.getTime();

	const [newOpen, setNewOpen] = useState(false);
	const [openDraftId, setOpenDraftId] = useState<string | null>(null);
	const [drawerOpen, setDrawerOpen] = useState(false);

	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const hrefFor = (id: string) => resourcePath(orgId, workspaceId, id);

	const knownIds = new Set(data.resources.map((r) => r.id));
	const visibleDrafts = drafts.drafts.filter((d) => d.resourceId === undefined || !knownIds.has(d.resourceId));

	const staleDraftIds = drafts.drafts
		.filter((d) => d.resourceId !== undefined && knownIds.has(d.resourceId) && !(drawerOpen && d.id === openDraftId))
		.map((d) => d.id)
		.join(",");
	const removeDraft = drafts.remove;
	useEffect(() => {
		if (staleDraftIds === "") return;
		for (const id of staleDraftIds.split(",")) removeDraft(id);
	}, [staleDraftIds, removeDraft]);

	const openDraft = (id: string) => {
		setOpenDraftId(id);
		setDrawerOpen(true);
	};

	const addDraft = (image: string) => {
		const taken = new Set([...data.resources.map((r) => r.name), ...drafts.drafts.map((d) => d.name)]);
		const name = uniqueName(nameFromImage(image), taken);
		const defaultRegion = data.regions.find((r) => r.isDefault)?.region ?? data.regions[0]?.region ?? "";
		const id = `d${Date.now().toString(36)}`;
		drafts.add({
			id,
			name,
			image,
			region: defaultRegion,
			sub: name,
			port: DRAFT_DEFAULTS.port,
			cpu: DRAFT_DEFAULTS.cpu,
			memory: DRAFT_DEFAULTS.memory,
			min: DRAFT_DEFAULTS.min,
			max: DRAFT_DEFAULTS.max,
			cpuTarget: DRAFT_DEFAULTS.cpuTarget,
			vars: [{ key: "", value: "" }],
			hcPath: DRAFT_DEFAULTS.hcPath,
			hcInterval: DRAFT_DEFAULTS.hcInterval,
			hcTimeout: DRAFT_DEFAULTS.hcTimeout,
			hcFail: DRAFT_DEFAULTS.hcFail,
		});
		setNewOpen(false);
		openDraft(id);
	};

	const services: DiagramService[] = [
		...data.envResources.map((it) => ({
			key: it.resource.id,
			name: it.resource.name,
			status: effectiveResourceStatus(it.resource.status, !it.neverDeployed),
			domain: it.resource.domains.find((d) => d.isPrimary)?.domain ?? it.resource.domains[0]?.domain ?? null,
			regions: it.regions.map((g) => ({ region: g.region, replicas: g.replicas })),
			href: hrefFor(it.resource.id),
		})),
		...visibleDrafts.map((d) => ({
			key: d.id,
			name: d.name,
			status: "draft" as const,
			domain: d.sub !== "" ? d.sub : null,
			regions: [{ region: d.region, replicas: 0 }],
			onOpen: () => {
				openDraft(d.id);
			},
		})),
	];

	const otherSubs = new Set(drafts.drafts.filter((d) => d.id !== openDraftId).map((d) => d.sub.trim()));
	const openDraftValue = drafts.drafts.find((d) => d.id === openDraftId);
	const attention = buildAttention(data.envResources, hrefFor, nowMs);
	const loading = envsLoading || data.isLoading;
	const canCreate = active !== undefined && activeWorkspaceId !== null;
	const isEmpty = data.envResources.length === 0 && visibleDrafts.length === 0;
	const openNew = () => {
		setNewOpen(true);
	};

	const header = (
		<PageHeader
			title="Overview"
			actions={
				<CreateResourceMenu
					disabled={!canCreate}
					onService={openNew}
				/>
			}
		>
			{activeWorkspaceId !== null && (
				<EnvironmentMenu
					workspaceId={activeWorkspaceId}
					environments={environments}
					active={active}
					onSelect={setActive}
					usedEnvIds={data.usedEnvIds}
				/>
			)}
		</PageHeader>
	);

	if (data.error !== null) {
		return (
			<Page>
				{header}
				<Section>
					<EmptyState title="Couldn't load resources">
						{getErrorMessage(data.error, "Failed to load resources")}
					</EmptyState>
				</Section>
			</Page>
		);
	}

	if (!envsLoading && active === undefined) {
		return (
			<Page>
				{header}
				<Section>
					<EmptyState title="No environments yet">
						Create an environment from the menu next to the title to start deploying.
					</EmptyState>
				</Section>
			</Page>
		);
	}

	return (
		<Page>
			{header}
			{loading || active === undefined ? (
				<DashboardSkeleton />
			) : isEmpty ? (
				<Section>
					<EmptyState
						icon={<BoxIcon />}
						title={`Nothing in ${active.name} yet`}
						action={
							<Button onClick={openNew}>
								<PlusIcon />
								New service
							</Button>
						}
					>
						Deploy a container image to see its architecture, status and deployments here.
					</EmptyState>
				</Section>
			) : (
				<>
					<AttentionSection items={attention} />
					<ArchitectureDiagram services={services} regionOrder={data.regions.map((r) => r.region)} />
					<ResourcesTable
						key={active.id}
						items={data.envResources}
						regions={data.regions}
						orgId={orgId}
						workspaceId={workspaceId}
						envName={active.name}
						nowMs={nowMs}
					/>
					<RecentDeployments
						deployments={data.envDeployments}
						orgId={orgId}
						workspaceId={workspaceId}
						envName={active.name}
						nowMs={nowMs}
					/>
				</>
			)}
			<NewServiceDialog open={newOpen} onOpenChange={setNewOpen} onAdd={addDraft} />
			{active !== undefined && (
				<DraftDrawer
					draft={openDraftValue}
					open={drawerOpen}
					onOpenChange={setDrawerOpen}
					orgId={orgId}
					workspaceId={workspaceId}
					env={active}
					regions={data.regions}
					otherSubs={otherSubs}
					update={drafts.update}
					remove={drafts.remove}
				/>
			)}
		</Page>
	);
}
