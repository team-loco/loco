import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation } from "@connectrpc/connect-query";
import { deleteOrg, updateOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization, User } from "@gen/loco/org/v1/org_pb";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQueryClient } from "@tanstack/react-query";
import { LayersIcon, PlusIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { AccessLevel } from "@/hooks/useMyScopes";
import { toastConnectError } from "@/lib/error-handler";
import { workspacePath } from "@/lib/routes";

import { ConfirmDeleteDialog } from "./ConfirmDeleteDialog";
import { NewWorkspaceDialog } from "./NewWorkspaceDialog";
import { DangerCard, formatDay, NAME_RE, SavedBar, SaveBar, SettingsCard, SettingsRow } from "./parts";
import { formatResourceCount, type ResourceCount } from "./useResourceCounts";

export function OrgTab({
	org,
	workspaces,
	workspacesLoading,
	users,
	level,
	counts,
	countsLoading,
}: {
	org: Organization;
	workspaces: Workspace[];
	workspacesLoading: boolean;
	users: User[];
	level: AccessLevel;
	counts: Map<string, ResourceCount>;
	countsLoading: boolean;
}) {
	const queryClient = useQueryClient();
	const { clearContext } = useOrgWorkspace();
	const update = useMutation(updateOrg);
	const remove = useMutation(deleteOrg);

	const [draft, setDraft] = useState<string | null>(null);
	const [saved, setSaved] = useState(false);
	const [serverNameErr, setServerNameErr] = useState<string | null>(null);
	const [newOpen, setNewOpen] = useState(false);
	const [deleteOpen, setDeleteOpen] = useState(false);

	const name = draft ?? org.name;
	const nameT = name.trim();
	const dirty = name !== org.name;
	const nameErr =
		nameT === ""
			? "Name required"
			: nameT.length > 100
				? "Max 100 characters"
				: !NAME_RE.test(nameT)
					? "Lowercase letters, numbers and hyphens"
					: serverNameErr;
	const shownErr = dirty ? nameErr : null;
	const canWrite = level >= 2;
	const canAdmin = level >= 3;

	const withRes = workspaces.filter((w) => {
		const c = counts.get(w.id);
		return c !== undefined && (c.count > 0 || c.more);
	});
	const withResNames = withRes.map((w) => w.name);
	const lastName = withResNames.pop();
	const joinedNames =
		withResNames.length > 0 ? `${withResNames.join(", ")} and ${lastName ?? ""}` : (lastName ?? "");
	const subtitle = withRes.length > 0
		? `Delete the resources in ${joinedNames} first.`
		: "Empty workspaces are deleted with it.";
	const blockedReason = !canAdmin
		? `Requires Admin on ${org.name}`
		: countsLoading
			? "Checking workspaces for resources"
			: withRes.length > 0
				? "Workspaces still have resources"
				: null;
	const userName = (id: string) => {
		const u = users.find((x) => x.id === id);
		return u ? u.name || u.email : "";
	};

	const save = () => {
		if (nameErr !== null || update.isPending) return;
		update.mutate(
			{ orgId: org.id, name: nameT },
			{
				onSuccess: () => {
					setDraft(null);
					setSaved(true);
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
					if (err instanceof ConnectError && err.code === Code.AlreadyExists) {
						setServerNameErr(`${nameT} is taken`);
						return;
					}
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
			className="h-[30px] px-2.5 font-medium"
			disabled={!canWrite}
			onClick={() => {
				setNewOpen(true);
			}}
		>
			<PlusIcon />
			New workspace
		</Button>
	);

	return (
		<div className="flex flex-col gap-5">
			<SettingsCard>
				<SettingsRow label="Name" hint="Must be unique across loco.">
					<Input
						value={name}
						placeholder="team-loco"
						disabled={!canWrite}
						aria-invalid={shownErr !== null}
						className="h-9 max-w-[320px] text-[13.5px]"
						onChange={(e) => {
							setDraft(e.target.value.toLowerCase());
							setSaved(false);
							setServerNameErr(null);
						}}
					/>
					{shownErr !== null && <span className="text-sm text-bad-fg">{shownErr}</span>}
				</SettingsRow>
				<SettingsRow label="Created" last>
					<span className="text-fg2">{formatDay(org.createdAt)}</span>
				</SettingsRow>
				{dirty && canWrite && (
					<SaveBar
						onDiscard={() => {
							setDraft(null);
							setServerNameErr(null);
						}}
						onSave={save}
						disabled={nameErr !== null}
						pending={update.isPending}
					/>
				)}
				{saved && !dirty && <SavedBar />}
			</SettingsCard>

			<SettingsCard>
				<div className="flex items-center gap-2.5 border-b border-line px-5 py-3.5">
					<span className="flex-1 font-semibold">
						Workspaces <span className="font-normal text-fg3">{workspaces.length}</span>
					</span>
					{canWrite ? (
						newButton
					) : (
						<Tooltip>
							<TooltipTrigger render={<span className="inline-flex cursor-not-allowed" />}>{newButton}</TooltipTrigger>
							<TooltipContent>Requires Write on {org.name}</TooltipContent>
						</Tooltip>
					)}
				</div>
				{workspacesLoading &&
					[0, 1].map((i) => (
						<div key={i} className="border-b border-line px-5 py-3 last:border-b-0">
							<Skeleton className="h-9 w-full" />
						</div>
					))}
				{!workspacesLoading && workspaces.length === 0 && (
					<div className="px-5 py-7 text-fg3">No workspaces yet.</div>
				)}
				{workspaces.map((w) => {
					const by = userName(w.createdBy);
					const day = formatDay(w.createdAt);
					const resLabel = formatResourceCount(counts.get(w.id));
					return (
						<Link
							key={w.id}
							to={workspacePath(org.id, w.id)}
							className="grid grid-cols-[minmax(0,1fr)_100px] items-center gap-4 border-b border-line px-5 py-3 text-foreground no-underline last:border-b-0 hover:bg-bg2 md:grid-cols-[minmax(0,1fr)_130px_160px]"
						>
							<span className="flex min-w-0 items-center gap-2.5">
								<LayersIcon className="size-[15px] shrink-0 text-fg3" />
								<span className="flex min-w-0 flex-col">
									<span className="font-semibold">{w.name}</span>
									<span className="truncate text-sm text-fg3">{w.description || "—"}</span>
								</span>
							</span>
							<span className="text-fg2">{resLabel}</span>
							<span className="hidden text-[12.5px] text-fg3 md:inline">{by ? `${day} · ${by}` : day}</span>
						</Link>
					);
				})}
			</SettingsCard>

			<DangerCard
				title="Delete organization"
				subtitle={subtitle}
				subtitleWarn={withRes.length > 0}
				label={`Delete ${org.name}`}
				blockedReason={blockedReason}
				onClick={() => {
					setDeleteOpen(true);
				}}
			/>

			<NewWorkspaceDialog open={newOpen} onOpenChange={setNewOpen} orgId={org.id} workspaces={workspaces} />
			<ConfirmDeleteDialog
				open={deleteOpen}
				onOpenChange={setDeleteOpen}
				title={`Delete ${org.name}`}
				text={`This removes ${org.name}, its ${workspaces.length.toString()} empty ${workspaces.length === 1 ? "workspace" : "workspaces"} and all members.`}
				target={org.name}
				confirmLabel="Delete organization"
				pending={remove.isPending}
				onConfirm={confirmDelete}
			/>
		</div>
	);
}
