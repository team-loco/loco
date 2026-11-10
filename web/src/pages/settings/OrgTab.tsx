import { useMutation } from "@connectrpc/connect-query";
import { deleteOrg, updateOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization, User } from "@gen/loco/org/v1/org_pb";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronRightIcon, PlusIcon } from "lucide-react";
import { useState, type MouseEvent } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { ConfirmDeleteDialog } from "@/components/design/ConfirmDeleteDialog";
import { CopyId } from "@/components/design/CopyId";
import { DangerZone } from "@/components/design/DangerZone";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SaveBar } from "@/components/design/SaveBar";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { CreateScopeDialog } from "@/components/shell/CreateScopeDialog";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { AccessLevel } from "@/hooks/useMyScopes";
import { toastConnectError } from "@/lib/error-handler";
import { formatCount, pluralize } from "@/lib/format";
import { usePagedRows } from "@/lib/paging";
import { settingsPath } from "@/lib/routes";
import { userLabel } from "@/lib/user";

import { AuditLogTab } from "./AuditLogTab";
import { formatDay, KeyValues, SettingsCard, SettingsSection } from "./parts";
import { formatResourceCount, type ResourceCount } from "./useResourceCounts";

const MAX_NAME = 100;
const PAGE_SIZES = [25, 50, 100] as const;

export function OrgTab({
	org,
	workspaces,
	workspacesLoading,
	users,
	level,
	counts,
	countsLoading,
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
	counts: Map<string, ResourceCount>;
	countsLoading: boolean;
	currentWorkspaceId: string;
	draft: string | null;
	onDraft: (name: string | null) => void;
	onOpenWorkspace: (event: MouseEvent, workspace: Workspace) => void;
	memberCount: number | undefined;
}) {
	const queryClient = useQueryClient();
	const { clearContext } = useOrgWorkspace();
	const update = useMutation(updateOrg);
	const remove = useMutation(deleteOrg);

	const [query, setQuery] = useState("");
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

	const withRes = workspaces.filter((w) => {
		const c = counts.get(w.id);
		return c !== undefined && (c.count > 0 || c.more);
	});
	const withResNames = withRes.map((w) => w.name);
	const lastName = withResNames.pop();
	const joinedNames = withResNames.length > 0 ? `${withResNames.join(", ")} and ${lastName ?? ""}` : (lastName ?? "");
	const subtitle =
		withRes.length > 0 ? `Delete the resources in ${joinedNames} first.` : "Empty workspaces are deleted with it.";
	const blockedReason = !canAdmin
		? `Requires Admin on ${org.name}`
		: countsLoading
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

	const newButton = (
		<Button
			variant="outline"
			size="sm"
			disabled={!canWrite}
			onClick={() => {
				setNewOpen(true);
			}}
		>
			<PlusIcon />
			New workspace
		</Button>
	);
	const creator = userName(org.createdBy);
	const createdDay = formatDay(org.createdAt);

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
							className="h-9 max-w-[420px] font-mono text-[13.5px]"
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
									<Link
										key="members"
										to={`/org/${org.id}/team`}
										className="text-foreground underline underline-offset-3"
									>
										{memberCount === 1 ? "1 person" : `${formatCount(memberCount)} people`}
									</Link>
								),
							],
						]}
					/>
				</SettingsCard>
			</SettingsSection>

			<Section
				title="Workspaces"
				count={workspaces.length}
				className="max-w-[960px]"
				actions={
					canWrite ? (
						newButton
					) : (
						<Tooltip>
							<TooltipTrigger render={<span className="inline-flex" />}>{newButton}</TooltipTrigger>
							<TooltipContent>Requires Write on {org.name}</TooltipContent>
						</Tooltip>
					)
				}
				search={
					<SearchInput
						aria-label="Search workspaces"
						placeholder="Search workspaces"
						value={query}
						onChange={(e) => {
							setQuery(e.target.value);
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
					<Table flush>
						<TableHeader>
							<TableRow>
								<TableHead>Name</TableHead>
								<TableHead className="hidden md:table-cell">Description</TableHead>
								<TableHead>Resources</TableHead>
								<TableHead className="hidden md:table-cell">Created</TableHead>
								<TableHead className="w-6">
									<span className="sr-only">Open</span>
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{paged.rows.map((w) => {
								const by = userName(w.createdBy);
								const day = formatDay(w.createdAt);
								return (
									<TableRow key={w.id} className="relative hover:bg-panel2 [&>td]:h-[52px]">
										<TableCell>
											<Link
												to={settingsPath(org.id, w.id, "workspace")}
												className="font-mono text-[13px] text-foreground no-underline after:absolute after:inset-0"
												onClick={(e) => {
													onOpenWorkspace(e, w);
												}}
											>
												{w.name}
											</Link>
											{w.id === currentWorkspaceId && (
												<Badge tone="pill" className="ml-2">
													current
												</Badge>
											)}
										</TableCell>
										<TableCell className="hidden max-w-0 truncate text-[12.5px] text-fg2 md:table-cell">
											{w.description || "—"}
										</TableCell>
										<TableCell className="text-[12.5px] text-fg2">{formatResourceCount(counts.get(w.id))}</TableCell>
										<TableCell className="hidden text-[12px] text-fg3 md:table-cell">
											{by === "" ? day : `${day} · ${by}`}
										</TableCell>
										<TableCell className="text-fg3">
											<ChevronRightIcon className="size-3.5" />
										</TableCell>
									</TableRow>
								);
							})}
						</TableBody>
					</Table>
				)}
				<Pager {...paged.pager} {...paged.ends} />
			</Section>

			{canAdmin && <AuditLogTab org={org} workspaces={workspaces} users={users} />}

			<SettingsSection title="Danger zone">
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
