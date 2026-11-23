import type { TimePreferences } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { deleteOrg, updateOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization, User } from "@gen/loco/org/v1/org_pb";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { keepPreviousData, useQueryClient } from "@tanstack/react-query";
import { PlusIcon } from "lucide-react";
import { useState } from "react";
import { parseAsNumberLiteral, parseAsString, useQueryStates } from "nuqs";
import { Link } from "react-router";
import { toast } from "@/lib/toast";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { ConfirmDeleteDialog } from "@/components/design/ConfirmDeleteDialog";
import { CopyId } from "@/components/design/CopyId";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { DangerZone } from "@/components/design/DangerZone";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { LimitedAction, LimitUsageCount } from "@/components/design/LimitedAction";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SaveBar } from "@/components/design/SaveBar";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { CreateScopeDialog } from "@/components/shell/CreateScopeDialog";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { AccessLevel } from "@/hooks/useMyScopes";
import { useOrgLimitDetails } from "@/hooks/useOrgLimits";
import { useTokenPage } from "@/hooks/useTokenPage";
import { toastConnectError } from "@/lib/error-handler";
import { formatCount, pluralize } from "@/lib/format";
import { limitReason, limitUsage } from "@/lib/limits";
import { pageTokensParser, tokenPageLabel, tokenPager } from "@/lib/paging";
import { settingsPath } from "@/lib/routes";
import { userLabel } from "@/lib/user";

import { LimitsSection } from "./LimitsSection";
import { formatDay, KeyValues, SettingsCard, SettingsSection } from "./parts";

const MAX_NAME = 100;
const PAGE_SIZES = [25, 50, 100] as const;
const PAGE_PARAMS = {
	q: parseAsString.withDefault(""),
	pages: pageTokensParser,
	size: parseAsNumberLiteral(PAGE_SIZES).withDefault(PAGE_SIZES[0]),
};
// The delete-organization notice names at most this many workspaces that still hold resources.
const NAMED_WITH_RESOURCES = 10;

function workspaceColumns({
	orgId,
	currentWorkspaceId,
	onOpenWorkspace,
	timePref,
}: {
	orgId: string;
	currentWorkspaceId: string;
	onOpenWorkspace: (workspace: Workspace) => void;
	timePref: TimePreferences;
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
									if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
									e.preventDefault();
									onOpenWorkspace(w);
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
			cell: ({ row: { original: w } }) => (
				<CellText text={formatDay(w.createdAt, timePref)} className="font-mono text-[12px] text-fg3" />
			),
		},
	];
}

export function OrgTab({
	org,
	currentWorkspace,
	users,
	level,
	currentWorkspaceId,
	draft,
	onDraft,
	onOpenWorkspace,
	memberCount,
}: {
	org: Organization;
	currentWorkspace: Workspace | undefined;
	users: User[];
	level: AccessLevel;
	currentWorkspaceId: string;
	draft: string | null;
	onDraft: (name: string | null) => void;
	/** Switches the page to the workspace's settings. */
	onOpenWorkspace: (workspace: Workspace) => void;
	memberCount: number | undefined;
}) {
	const timePref = useTimePreferences();
	const queryClient = useQueryClient();
	const { clearContext } = useOrgWorkspace();
	const limitDetails = useOrgLimitDetails(org.id);
	const limits = limitDetails?.limits;
	const update = useMutation(updateOrg);
	const remove = useMutation(deleteOrg);

	const [params, setParams] = useQueryStates(PAGE_PARAMS, { history: "replace" });
	const listed = useTokenPage(
		listOrgWorkspaces,
		{ orgId: org.id, pageSize: params.size, query: params.q.trim() },
		params.pages,
		{ placeholderData: keepPreviousData },
	);
	const rows = listed.query.data?.workspaces ?? [];
	const pager = tokenPager(params.pages, listed.nextPageToken, (pages) => {
		void setParams({ pages });
	});
	const counted = useQuery(listOrgWorkspaces, {
		orgId: org.id,
		pageSize: 1,
		query: "",
		pageToken: "",
		includeTotalCount: true,
	});
	const workspaceCount = counted.data?.totalCount;
	const withResources = useQuery(listOrgWorkspaces, {
		orgId: org.id,
		hasResources: true,
		pageSize: NAMED_WITH_RESOURCES,
		includeTotalCount: true,
	});
	const [newOpen, setNewOpen] = useState(false);
	const [deleteOpen, setDeleteOpen] = useState(false);

	const name = draft ?? org.name;
	const nameT = name.trim();
	const dirty = name !== org.name;
	const nameErr = nameT === "" || nameT.length > MAX_NAME ? `Use 1–${MAX_NAME.toString()} characters.` : null;
	const shownErr = dirty ? nameErr : null;
	const canWrite = level >= 2;
	const canAdmin = level >= 3;

	const withResCount = withResources.data?.totalCount ?? 0;
	const withResNames = (withResources.data?.workspaces ?? []).map((w) => w.name);
	const unnamed = withResCount - withResNames.length;
	const lastName = unnamed > 0 ? `${formatCount(unnamed)} more` : withResNames.pop();
	const joinedNames = withResNames.length > 0 ? `${withResNames.join(", ")} and ${lastName ?? ""}` : (lastName ?? "");
	const subtitle =
		withResCount > 0 ? `Delete the resources in ${joinedNames} first.` : "Empty workspaces are deleted with it.";
	const blockedReason = !canAdmin
		? `Requires Admin on ${org.name}`
		: withResources.isPending
			? "Checking workspaces for resources"
			: withResCount > 0
				? "Workspaces still have resources"
				: null;
	const userName = (id: string) => {
		const u = users.find((x) => x.id === id);
		return u ? userLabel(u) : "";
	};

	const table = useDataTable("org-workspaces", {
		data: rows,
		getRowId: (w) => w.id,
		columns: workspaceColumns({ orgId: org.id, currentWorkspaceId, onOpenWorkspace, timePref }),
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

	const workspaceUsage =
		workspaceCount === undefined
			? null
			: limitUsage(workspaceCount, limits?.workspacesPerOrg, "workspaces", "organization");
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
									title="Copy ID"
									toastText="Copied ID"
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
									<Button
										key="members"
										variant="link"
										className="h-auto p-0 text-[13px]"
										nativeButton={false}
										render={<Link to={`/org/${org.id}/team`} />}
									>
										{memberCount === 1 ? "1 person" : `${formatCount(memberCount)} people`}
									</Button>
								),
							],
						]}
					/>
				</SettingsCard>
			</SettingsSection>

			<Section
				title="Workspaces"
				count={workspaceUsage === null ? workspaceCount : <LimitUsageCount usage={workspaceUsage} />}
				className="max-w-[960px]"
				actions={<LimitedAction reason={newBlocked}>{newButton}</LimitedAction>}
			>
				<div className="flex justify-end px-3 py-2.5">
					<div className="max-w-[280px] flex-[1_1_160px] max-sm:max-w-none">
						<SearchInput
							aria-label="Search workspaces"
							placeholder="Search workspaces"
							value={params.q}
							onChange={(e) => {
								void setParams({ q: e.target.value, pages: null });
							}}
						/>
					</div>
				</div>
				{listed.query.isPending ? (
					<div className="flex flex-col gap-2 px-5 py-3">
						<Skeleton className="h-9 w-full" />
						<Skeleton className="h-9 w-full" />
					</div>
				) : rows.length === 0 ? (
					<div className="px-5 py-7 text-fg3">
						{workspaceCount === 0 ? "No workspaces yet." : "No workspaces match."}
					</div>
				) : (
					<DataTable
						table={table}
						label="Workspaces"
						band
						onRowClick={({ original: w }) => {
							onOpenWorkspace(w);
						}}
						rowAction="navigate"
					/>
				)}
				<Pager
					label={tokenPageLabel(pager.page, params.size, rows.length, listed.total)}
					page={pager.page}
					total={rows.length}
					pageSizes={PAGE_SIZES}
					pageSize={params.size}
					onPageSize={(size) => {
						void setParams({ size: PAGE_SIZES.find((n) => n === size) ?? null, pages: null });
					}}
					onFirst={pager.onFirst}
					onPrev={pager.onPrev}
					onNext={pager.onNext}
					className="pt-3 pb-3.5"
				/>
			</Section>

			<LimitsSection
				org={org}
				limits={limitDetails}
				workspaceCount={workspaceCount}
				memberCount={memberCount}
				workspace={currentWorkspace}
			/>

			<SettingsSection id="organization-danger" title="Danger zone">
				<DangerZone
					title={`Delete ${org.name}`}
					description={subtitle}
					warn={withResCount > 0}
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
				title={`Delete ${org.name}?`}
				text={`This permanently deletes ${org.name}, its ${pluralize(workspaceCount ?? 0, "empty workspace")} and all members.`}
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
