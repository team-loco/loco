import { useTimePreferences } from "@/lib/use-time-preferences";
import { useMutation } from "@connectrpc/connect-query";
import { deleteOrg, updateOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization, User } from "@gen/loco/org/v1/org_pb";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQueryClient } from "@tanstack/react-query";
import { PlusIcon } from "lucide-react";
import { useState } from "react";
import { parseAsString, useQueryState } from "nuqs";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { ConfirmDeleteDialog } from "@/components/design/ConfirmDeleteDialog";
import { CopyId } from "@/components/design/CopyId";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { DangerZone } from "@/components/design/DangerZone";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { LimitedAction } from "@/components/design/LimitedAction";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SaveBar } from "@/components/design/SaveBar";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { CreateScopeDialog } from "@/components/shell/CreateScopeDialog";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { AccessLevel } from "@/hooks/useMyScopes";
import { useOrgLimitDetails } from "@/hooks/useOrgLimits";
import { toastConnectError } from "@/lib/error-handler";
import { formatCount, pluralize } from "@/lib/format";
import { limitReason, limitUsage, usageLabel } from "@/lib/limits";
import { usePagedRows } from "@/lib/paging";
import { settingsPath } from "@/lib/routes";
import { userLabel } from "@/lib/user";

import { LimitsSection } from "./LimitsSection";
import { formatDay, KeyValues, SettingsCard, SettingsSection } from "./parts";

const MAX_NAME = 100;
const PAGE_SIZES = [25, 50, 100] as const;

function workspaceColumns({
	orgId,
	currentWorkspaceId,
	onOpenWorkspace,
	createdLabel,
}: {
	orgId: string;
	currentWorkspaceId: string;
	onOpenWorkspace: (workspace: Workspace) => boolean;
	createdLabel: (workspace: Workspace) => string;
}): DataColumn<Workspace>[] {
	return [
		{
			id: "name",
			header: "Name",
			size: 160,
			minSize: 100,
			meta: { primary: true },
			cell: ({ row: { original: w } }) => (
				<span className="flex min-w-0 items-center gap-2">
					<CellText
						text={w.name}
						render={
							<Link
								to={settingsPath(orgId, w.id, "workspace")}
								onClick={(e) => {
									if (onOpenWorkspace(w)) e.preventDefault();
								}}
							/>
						}
						className="font-mono text-[13px] text-foreground no-underline"
					/>
					{w.id === currentWorkspaceId && <Badge tone="pill">current</Badge>}
				</span>
			),
		},
		{
			id: "description",
			header: "Description",
			size: 240,
			minSize: 100,
			meta: { hideOnMobile: true, grow: true },
			cell: ({ row: { original: w } }) => <CellText text={w.description || "—"} className="text-[12.5px] text-fg2" />,
		},
		{
			id: "resources",
			header: "Resources",
			size: 120,
			minSize: 90,
			meta: { cellClassName: "text-[12.5px] text-fg2" },
			cell: ({ row: { original: w } }) => formatResourceCount(w.resourceCount),
		},
		{
			id: "created",
			header: "Created",
			size: 220,
			minSize: 100,
			meta: { hideOnMobile: true },
			cell: ({ row: { original: w } }) => <CellText text={createdLabel(w)} className="text-[12px] text-fg3" />,
		},
	];
}

export function OrgTab({
	org,
	workspaces,
	workspacesLoading,
	users,
	level,
	currentWorkspaceId,
	draft,
	onDraft,
	onOpenWorkspace,
	memberCount,
}: {
	org: Organization;
	workspaces: Workspace[];
	workspacesLoading: boolean;
	users: User[];
	level: AccessLevel;
	currentWorkspaceId: string;
	draft: string | null;
	onDraft: (name: string | null) => void;
	/** Opens the workspace in place and reports whether it did; otherwise the caller navigates to its settings. */
	onOpenWorkspace: (workspace: Workspace) => boolean;
	memberCount: number | undefined;
}) {
	const timePref = useTimePreferences();
	const queryClient = useQueryClient();
	const { clearContext } = useOrgWorkspace();
	const limitDetails = useOrgLimitDetails(org.id);
	const limits = limitDetails?.limits;
	const update = useMutation(updateOrg);
	const remove = useMutation(deleteOrg);

	const [query, setQuery] = useQueryState("q", parseAsString.withDefault("").withOptions({ history: "replace" }));
	const [newOpen, setNewOpen] = useState(false);
	const [deleteOpen, setDeleteOpen] = useState(false);

	const name = draft ?? org.name;
	const nameT = name.trim();
	const dirty = name !== org.name;
	const nameErr =
		nameT === "" ? "Name required" : nameT.length > MAX_NAME ? `Max ${MAX_NAME.toString()} characters` : null;
	const shownErr = dirty ? nameErr : null;
	const canWrite = level >= 2;
	const canAdmin = level >= 3;

	const withRes = workspaces.filter((w) => (w.resourceCount ?? 0) > 0);
	const withResNames = withRes.map((w) => w.name);
	const lastName = withResNames.pop();
	const joinedNames = withResNames.length > 0 ? `${withResNames.join(", ")} and ${lastName ?? ""}` : (lastName ?? "");
	const subtitle =
		withRes.length > 0 ? `Delete the resources in ${joinedNames} first.` : "Empty workspaces are deleted with it.";
	const blockedReason = !canAdmin
		? `Requires Admin on ${org.name}`
		: workspacesLoading
			? "Checking workspaces for resources"
			: withRes.length > 0
				? "Workspaces still have resources"
				: null;
	const needle = query.trim().toLowerCase();
	const matches = workspaces.filter(
		(w) => w.name.toLowerCase().includes(needle) || w.description.toLowerCase().includes(needle),
	);
	const paged = usePagedRows(matches, PAGE_SIZES);
	const userName = (id: string) => {
		const u = users.find((x) => x.id === id);
		return u ? userLabel(u) : "";
	};

	const navigate = useNavigate();
	const createdLabel = (w: Workspace) => {
		const by = userName(w.createdBy);
		const day = formatDay(w.createdAt, timePref);
		return by === "" ? day : `${day} · ${by}`;
	};
	const table = useDataTable("org-workspaces", {
		data: paged.rows,
		getRowId: (w) => w.id,
		columns: workspaceColumns({ orgId: org.id, currentWorkspaceId, onOpenWorkspace, createdLabel }),
	});

	const save = () => {
		if (nameErr !== null || update.isPending) return;
		update.mutate(
			{ orgId: org.id, name: nameT },
			{
				onSuccess: () => {
					onDraft(null);
					toast.success("Saved");
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to update organization");
				},
			},
		);
	};

	const confirmDelete = () => {
		remove.mutate(
			{ orgId: org.id },
			{
				onSuccess: () => {
					toast.success(`Deleted ${org.name}`);
					setDeleteOpen(false);
					void queryClient.invalidateQueries();
					clearContext();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to delete organization");
				},
			},
		);
	};

	const workspaceUsage = workspacesLoading
		? null
		: limitUsage(workspaces.length, limits?.workspacesPerOrg, "workspaces", "organization");
	const newBlocked = canWrite ? limitReason(workspaceUsage) : `Requires Write on ${org.name}`;
	const newButton = (
		<Button
			variant="outline"
			size="sm"
			disabled={newBlocked !== null}
			onClick={() => {
				setNewOpen(true);
			}}
		>
			<PlusIcon />
			New workspace
		</Button>
	);
	const creator = userName(org.createdBy);
	const createdDay = formatDay(org.createdAt, timePref);

	return (
		<div className="flex flex-col gap-[22px]">
			<SettingsSection title="General">
				<SettingsCard className="flex flex-col gap-4 px-5 py-[18px]">
					<Field label="Name" error={shownErr}>
						<Input
							value={name}
							placeholder="team-loco"
							disabled={!canWrite}
							aria-invalid={shownErr !== null}
							className="h-9 font-mono text-[13.5px]"
							onChange={(e) => {
								onDraft(e.target.value);
							}}
						/>
					</Field>
					<KeyValues
						items={[
							[
								"ID",
								<CopyId
									key="id"
									value={org.id}
									className="h-6 max-w-full bg-panel2 px-2 font-mono text-[12px] font-normal"
								>
									<span className="truncate">{org.id}</span>
								</CopyId>,
							],
							["Created", creator === "" ? createdDay : `${createdDay} by ${creator}`],
							[
								"Members",
								memberCount === undefined ? (
									"—"
								) : (
									<Link key="members" to={`/org/${org.id}/team`} className="text-foreground hover:text-fg2">
										{memberCount === 1 ? "1 person" : `${formatCount(memberCount)} people`} →
									</Link>
								),
							],
						]}
					/>
				</SettingsCard>
			</SettingsSection>

			<Section
				title="Workspaces"
				count={workspaceUsage === null ? workspaces.length : usageLabel(workspaceUsage)}
				className="max-w-[960px]"
				actions={<LimitedAction reason={newBlocked}>{newButton}</LimitedAction>}
				search={
					<SearchInput
						aria-label="Search workspaces"
						placeholder="Search workspaces"
						value={query}
						onChange={(e) => {
							void setQuery(e.target.value);
							paged.reset();
						}}
					/>
				}
			>
				{workspacesLoading ? (
					<div className="flex flex-col gap-2 px-5 py-3">
						<Skeleton className="h-9 w-full" />
						<Skeleton className="h-9 w-full" />
					</div>
				) : paged.rows.length === 0 ? (
					<div className="px-5 py-7 text-fg3">
						{workspaces.length === 0 ? "No workspaces yet." : "No workspaces match."}
					</div>
				) : (
					<DataTable
						table={table}
						label="Workspaces"
						onRowClick={({ original: w }) => {
							if (!onOpenWorkspace(w)) void navigate(settingsPath(org.id, w.id, "workspace"));
						}}
						rowAction="navigate"
					/>
				)}
				<Pager {...paged.pager} {...paged.ends} className="pt-3 pb-3.5" />
			</Section>

			<LimitsSection
				org={org}
				limits={limitDetails}
				workspaceCount={workspacesLoading ? undefined : workspaces.length}
				memberCount={memberCount}
			/>

			<SettingsSection id="organization-danger" title="Danger zone">
				<DangerZone
					title={`Delete ${org.name}`}
					description={subtitle}
					warn={withRes.length > 0}
					actionLabel="Delete organization"
					blockedReason={blockedReason}
					onAction={() => {
						setDeleteOpen(true);
					}}
				/>
			</SettingsSection>
			{dirty && canWrite && (
				<SaveBar
					onDiscard={() => {
						onDraft(null);
					}}
					onSave={save}
					saving={update.isPending}
					disabledReason={nameErr}
				/>
			)}

			<CreateScopeDialog kind={newOpen ? "workspace" : null} onOpenChange={setNewOpen} />
			<ConfirmDeleteDialog
				open={deleteOpen}
				onOpenChange={setDeleteOpen}
				title={`Delete ${org.name}`}
				text={`This removes ${org.name}, its ${pluralize(workspaces.length, "empty workspace")} and all members.`}
				target={org.name}
				confirmLabel="Delete organization"
				pending={remove.isPending}
				onConfirm={confirmDelete}
			/>
		</div>
	);
}

function formatResourceCount(count: number | undefined): string {
	if (count === undefined) return "—";
	return count === 0 ? "Empty" : pluralize(count, "resource");
}
