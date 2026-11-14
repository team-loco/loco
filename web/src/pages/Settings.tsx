import { useQuery, useTransport } from "@connectrpc/connect-query";
import { getOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { Building2Icon, LayersIcon, UserIcon } from "lucide-react";
import { useState, type MouseEvent } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";

import { useAuth } from "@/auth/AuthProvider";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { ErrorState } from "@/components/design/ErrorState";
import { Page, PageHeader } from "@/components/design/Page";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { orgMembersOptions } from "@/hooks/completeMembers";
import { ResourceAccess } from "@/components/ResourceAccess";
import { orgWorkspacesOptions } from "@/hooks/orgWorkspaces";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { settingsPath, type SettingsScope } from "@/lib/routes";

import { OrgTab } from "./settings/OrgTab";
import { PersonalTab } from "./settings/PersonalTab";
import { SCOPES, scopeFromParams } from "./settings/scope";
import { useResourceCounts } from "./settings/useResourceCounts";
import { SettingsSkeleton, type WorkspaceDraft, WorkspaceTab } from "./settings/WorkspaceTab";

interface Drafts {
	workspace: (WorkspaceDraft & { id: string }) | null;
	org: { id: string; name: string } | null;
	account: string | null;
}

const NO_DRAFTS: Drafts = { workspace: null, org: null, account: null };

function DirtyDot() {
	return (
		<>
			<span aria-hidden className="size-1.5 rounded-full bg-warn-fg" />
			<span className="sr-only">(unsaved changes)</span>
		</>
	);
}

export function Settings() {
	const { orgId = "", workspaceId = "" } = useParams<{ orgId: string; workspaceId: string }>();
	const [params, setParams] = useSearchParams();
	const navigate = useNavigate();
	const { user } = useAuth();
	const requested = scopeFromParams(params);
	const [drafts, setDrafts] = useState<Drafts>(NO_DRAFTS);
	const [leaveTo, setLeaveTo] = useState<string | null>(null);

	const transport = useTransport();
	const scopes = useMyScopes();
	const canReadOrg = !scopes.isLoading && scopes.orgMember(orgId);
	const readsWorkspace = (id: string) => !scopes.isLoading && scopes.workspaceLevel(orgId, id) >= 1;
	const workspaceQuery = useQuery(
		getWorkspace,
		{ workspaceId },
		{ enabled: workspaceId !== "" && readsWorkspace(workspaceId) },
	);
	const orgQuery = useQuery(getOrg, { orgId }, { enabled: orgId !== "" && canReadOrg });
	const wsQuery = useTanstackQuery({
		...orgWorkspacesOptions(transport, orgId),
		enabled: orgId !== "" && canReadOrg,
	});
	const membersQuery = useTanstackQuery({
		...orgMembersOptions(transport, orgId),
		enabled: orgId !== "" && canReadOrg,
	});
	const org = orgQuery.data?.organization;
	const workspaces = wsQuery.data?.workspaces ?? [];
	const users = (membersQuery.data?.members ?? []).flatMap((m) => (m.user ? [m.user] : []));
	const currentWs = workspaceQuery.data?.workspace ?? workspaces.find((w) => w.id === workspaceId);
	const workspaceIds = (canReadOrg ? workspaces.map((w) => w.id) : currentWs ? [currentWs.id] : []).filter(
		readsWorkspace,
	);
	const { counts, isLoading: countsLoading } = useResourceCounts(workspaceIds);
	const scope = requested === "org" && !canReadOrg ? "workspace" : requested;

	const wsDraft = drafts.workspace?.id === workspaceId ? drafts.workspace : null;
	const orgDraft = drafts.org?.id === orgId ? drafts.org : null;
	const wsDirty =
		wsDraft !== null &&
		currentWs !== undefined &&
		(wsDraft.name !== currentWs.name || wsDraft.desc !== currentWs.description);
	const orgDirty = orgDraft !== null && org !== undefined && orgDraft.name !== org.name;
	const accountDirty = drafts.account !== null && user !== null && drafts.account !== user.name;
	const anyDirty = wsDirty || orgDirty || accountDirty;

	const selectScope = (next: SettingsScope) => {
		const p = new URLSearchParams(params);
		p.delete("tab");
		p.set("scope", next);
		setParams(p, { replace: true });
	};

	const openWorkspace = (event: MouseEvent, w: Workspace) => {
		if (w.id === workspaceId) {
			event.preventDefault();
			selectScope("workspace");
		} else if (anyDirty) {
			event.preventDefault();
			setLeaveTo(settingsPath(orgId, w.id, "workspace"));
		}
	};

	const itemClass = "h-7! min-w-0 shrink gap-1.5 px-3 text-[12.5px]";

	return (
		<Page className="gap-[22px]">
			<PageHeader title="Settings" />
			<ToggleGroup
				variant="segmented"
				aria-label="Settings for"
				className="w-max max-w-full"
				value={[scope]}
				onValueChange={(v: string[]) => {
					const next = SCOPES.find((s) => s === v[0]);
					if (next !== undefined) selectScope(next);
				}}
			>
				{canReadOrg && (
					<ToggleGroupItem value="org" className={itemClass}>
						<Building2Icon className="size-[13px] shrink-0 text-fg3" />
						Organization
						{orgDirty && <DirtyDot />}
					</ToggleGroupItem>
				)}
				<ToggleGroupItem value="workspace" className={itemClass}>
					<LayersIcon className="size-[13px] shrink-0 text-fg3" />
					Workspace
					{wsDirty && <DirtyDot />}
				</ToggleGroupItem>
				<ToggleGroupItem value="account" className={itemClass}>
					<UserIcon className="size-[13px] shrink-0 text-fg3" />
					Personal
					{accountDirty && <DirtyDot />}
				</ToggleGroupItem>
			</ToggleGroup>

			{scope === "org" && orgQuery.error !== null && (
				<ErrorState error={orgQuery.error} what="settings" rpc={getOrg} scope="role" onRetry={orgQuery.refetch} />
			)}
			{((scopes.isLoading && scope !== "account") || (scope === "org" && orgQuery.error === null && !org)) && (
				<SettingsSkeleton />
			)}
			{!scopes.isLoading && scope === "workspace" && !readsWorkspace(workspaceId) && (
				<GrantedResources orgId={orgId} workspaceId={workspaceId} />
			)}
			{!scopes.isLoading && scope === "workspace" && readsWorkspace(workspaceId) && (
				<WorkspaceTab
					key={workspaceId}
					orgId={orgId}
					workspaceId={workspaceId}
					workspaces={workspaces}
					users={users}
					level={scopes.workspaceLevel(orgId, workspaceId)}
					resourceCount={counts.get(workspaceId)}
					draft={wsDraft}
					onDraft={(d) => {
						setDrafts({ ...drafts, workspace: d === null ? null : { ...d, id: workspaceId } });
					}}
				/>
			)}
			{scope === "account" && user !== null && (
				<PersonalTab
					user={user}
					draft={drafts.account}
					onDraft={(name) => {
						setDrafts({ ...drafts, account: name });
					}}
				/>
			)}
			{org && scope === "org" && (
				<OrgTab
					key={org.id}
					org={org}
					workspaces={workspaces}
					workspacesLoading={wsQuery.isLoading}
					users={users}
					level={scopes.orgLevel(org.id)}
					counts={counts}
					countsLoading={countsLoading}
					currentWorkspaceId={workspaceId}
					memberCount={membersQuery.data?.members.length}
					draft={orgDraft?.name ?? null}
					onDraft={(name) => {
						setDrafts({ ...drafts, org: name === null ? null : { id: org.id, name } });
					}}
					onOpenWorkspace={openWorkspace}
				/>
			)}

			<ConfirmDialog
				open={leaveTo !== null}
				title="Discard unsaved changes?"
				description="The changes you made here are not saved. Leaving this page drops them."
				confirmLabel="Discard and leave"
				cancelLabel="Keep editing"
				onConfirm={() => {
					const to = leaveTo;
					setDrafts(NO_DRAFTS);
					setLeaveTo(null);
					if (to !== null) void navigate(to);
				}}
				onClose={() => {
					setLeaveTo(null);
				}}
			/>
		</Page>
	);
}

function GrantedResources({ orgId, workspaceId }: { orgId: string; workspaceId: string }) {
	const { data, isLoading } = useTanstackQuery(useWorkspaceResourcesOptions(orgId, workspaceId));
	if (isLoading) return <SettingsSkeleton />;
	return <ResourceAccess orgId={orgId} workspaceId={workspaceId} resources={data?.resources ?? []} />;
}

export function PersonalSettings() {
	const { user } = useAuth();
	const [draft, setDraft] = useState<string | null>(null);
	return (
		<Page className="gap-[22px]">
			<PageHeader title="Settings" />
			{user !== null && <PersonalTab user={user} draft={draft} onDraft={setDraft} />}
		</Page>
	);
}
