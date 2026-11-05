import { useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { useState } from "react";

import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage } from "@/lib/error-handler";
import { resourcePath } from "@/lib/routes";
import { ArchitectureDiagram, type DiagramService } from "@/pages/dashboard/ArchitectureDiagram";
import { AttentionSection, buildAttention } from "@/pages/dashboard/AttentionSection";
import { CreateResourceMenu } from "@/pages/dashboard/CreateResourceMenu";
import { DraftDrawer } from "@/pages/dashboard/DraftDrawer";
import { FirstServiceEmpty } from "@/pages/dashboard/FirstServiceEmpty";
import { draftSizing, isPrivate, nameFromImage, uniqueName, useDrafts } from "@/pages/dashboard/drafts";
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
	const { activeOrgId, activeWorkspaceId, workspaces } = useOrgWorkspace();
	const { environments, active, setActive, isLoading: envsLoading } = useEnvironments();
	const configQuery = useQuery(getConfig, {});
	const serviceDefaults = configQuery.data?.serviceDefaults;
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

	const changeDrawerOpen = (open: boolean) => {
		setDrawerOpen(open);
		if (open) return;
		for (const d of drafts.drafts) {
			if (d.resourceId !== undefined && knownIds.has(d.resourceId)) drafts.remove(d.id);
		}
	};

	const openDraft = (id: string) => {
		setOpenDraftId(id);
		setDrawerOpen(true);
	};

	const defaultRegion = data.regions.find((r) => r.isDefault)?.region ?? data.regions[0]?.region ?? "";
	const workspaceName = workspaces.find((w) => w.id === activeWorkspaceId)?.name ?? "";
	const otherEnv = environments.find((e) => e.id !== active?.id);

	const addDraft = (image: string) => {
		if (serviceDefaults === undefined) return;
		const taken = new Set([...data.resources.map((r) => r.name), ...drafts.drafts.map((d) => d.name)]);
		const name = uniqueName(nameFromImage(image), taken);
		const id = `d${Date.now().toString(36)}`;
		drafts.add({
			id,
			name,
			image,
			region: defaultRegion,
			sub: name,
			...draftSizing(serviceDefaults),
			vars: [{ key: "", value: "" }],
		});
		setNewOpen(false);
		openDraft(id);
	};

	const services: DiagramService[] = [
		...data.envResources.map((it) => ({
			key: it.resource.id,
			name: it.resource.name,
			status: it.status,
			domain: it.resource.domains.find((d) => d.isPrimary)?.domain ?? it.resource.domains[0]?.domain ?? null,
			regions: it.regions.map((g) => ({ region: g.region, replicas: g.desired })),
			href: hrefFor(it.resource.id),
		})),
		...visibleDrafts.map((d) => ({
			key: d.id,
			name: d.name,
			status: "draft" as const,
			domain: !isPrivate(d) && d.sub !== "" ? d.sub : null,
			regions: [{ region: d.region, replicas: 0 }],
			onOpen: () => {
				openDraft(d.id);
			},
		})),
	];

	const otherSubs = new Set(
		drafts.drafts.filter((d) => d.id !== openDraftId && !isPrivate(d)).map((d) => d.sub.trim()),
	);
	const openDraftValue = drafts.drafts.find((d) => d.id === openDraftId);
	const attention = buildAttention(data.envResources, hrefFor, nowMs);
	const loading = envsLoading || data.isLoading || configQuery.isPending;
	const canCreate = active !== undefined && activeWorkspaceId !== null;
	const isEmpty = data.envResources.length === 0 && visibleDrafts.length === 0;
	const openNew = () => {
		setNewOpen(true);
	};

	const header = (
		<PageHeader
			title="Overview"
			actions={loading ? undefined : <CreateResourceMenu disabled={!canCreate} onService={openNew} />}
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

	const loadError = data.error ?? configQuery.error;
	if (loadError !== null) {
		return (
			<Page>
				{header}
				<Section>
					<EmptyState title="Couldn't load resources">
						{getErrorMessage(loadError, "Failed to load resources")}
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
				<FirstServiceEmpty
					scope={`${workspaceName} / ${active.name}`}
					environmentId={active.id}
					region={defaultRegion}
					otherEnvName={otherEnv?.name ?? null}
					onSwitchEnv={() => {
						if (otherEnv !== undefined) setActive(otherEnv);
					}}
					onImage={addDraft}
				/>
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
					onOpenChange={changeDrawerOpen}
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
