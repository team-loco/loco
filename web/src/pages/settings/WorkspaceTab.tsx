import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import type { User } from "@gen/loco/org/v1/org_pb";
import {
	deleteWorkspace,
	getWorkspace,
	listWorkspaceMembers,
	updateWorkspace,
} from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { Textarea } from "@/components/design/Textarea";
import type { AccessLevel } from "@/hooks/useMyScopes";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { pluralize } from "@/lib/format";
import { workspacePath } from "@/lib/routes";
import { cn } from "@/lib/utils";

import { ConfirmDeleteDialog } from "./ConfirmDeleteDialog";
import { DangerCard, formatDay, NAME_RE, SavedBar, SaveBar, SettingsCard, SettingsRow } from "./parts";
import type { ResourceCount } from "./useResourceCounts";

interface Draft {
	name: string;
	desc: string;
}

export function WorkspaceTab({
	orgId,
	orgName,
	workspaceId,
	workspaces,
	users,
	level,
	resourceCount,
}: {
	orgId: string;
	orgName: string;
	workspaceId: string;
	workspaces: Workspace[];
	users: User[];
	level: AccessLevel;
	resourceCount: ResourceCount | undefined;
}) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const { data, isLoading, error } = useQuery(getWorkspace, { workspaceId });
	const { data: membersRes } = useQuery(listWorkspaceMembers, { workspaceId, pageSize: 200 });
	const update = useMutation(updateWorkspace);
	const remove = useMutation(deleteWorkspace);

	const [draft, setDraft] = useState<Draft | null>(null);
	const [saved, setSaved] = useState(false);
	const [serverNameErr, setServerNameErr] = useState<string | null>(null);
	const [deleteOpen, setDeleteOpen] = useState(false);

	const ws = data?.workspace;
	if (isLoading) return <SettingsSkeleton />;
	if (!ws) {
		const message = getErrorMessage(error, "Failed to load workspace");
		return <div className="rounded-lg border border-line px-5 py-8 text-fg3">{message}</div>;
	}

	const current: Draft = draft ?? { name: ws.name, desc: ws.description };
	const nameT = current.name.trim();
	const dirty = current.name !== ws.name || current.desc !== ws.description;
	const taken = workspaces.some((w) => w.id !== ws.id && w.name === nameT);
	const nameErr =
		nameT === ""
			? "Name required"
			: nameT.length > 63
				? "Max 63 characters"
				: !NAME_RE.test(nameT)
					? "Lowercase letters, numbers and hyphens"
					: taken
						? `${nameT} already exists in ${orgName}`
						: serverNameErr;
	const canWrite = level >= 2;
	const canAdmin = level >= 3;
	const shownErr = dirty ? nameErr : null;
	const creator = users.find((u) => u.id === ws.createdBy);
	const createdDay = formatDay(ws.createdAt);
	const created = creator ? `${createdDay} by ${creator.name || creator.email}` : createdDay;

	const nRes = resourceCount?.count ?? 0;
	const hasRes = nRes > 0 || resourceCount?.more === true;
	const members = membersRes?.members.length ?? 0;
	const resLabel = resourceCount === undefined ? "—" : `${nRes.toString()}${resourceCount.more ? "+" : ""}`;
	const subtitle = hasRes
		? `Delete the ${resLabel} ${nRes === 1 ? "resource" : "resources"} in ${ws.name} first.`
		: `${resLabel} resources, ${pluralize(members, "member")} with direct access`;
	const blockedReason = !canAdmin
		? `Requires Admin on ${ws.name}`
		: hasRes
			? "Workspace still has resources"
			: null;

	const save = () => {
		if (nameErr !== null || update.isPending) return;
		update.mutate(
			{ workspaceId: ws.id, name: nameT, description: current.desc },
			{
				onSuccess: () => {
					setDraft(null);
					setSaved(true);
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
					if (err instanceof ConnectError && err.code === Code.AlreadyExists) {
						setServerNameErr(`${nameT} already exists in ${orgName}`);
						return;
					}
					toastConnectError(err, "Failed to update workspace");
				},
			},
		);
	};

	const confirmDelete = () => {
		remove.mutate(
			{ workspaceId: ws.id },
			{
				onSuccess: () => {
					toast.success(`Deleted ${ws.name}`);
					setDeleteOpen(false);
					const next = workspaces.find((w) => w.id !== ws.id);
					void queryClient.invalidateQueries();
					void navigate(next ? workspacePath(orgId, next.id) : "/organizations");
				},
				onError: (err) => {
					toastConnectError(err, "Failed to delete workspace");
				},
			},
		);
	};

	return (
		<div className="flex flex-col gap-5">
			<SettingsCard>
				<SettingsRow label="Name" hint={`Used in URLs and the CLI. Must be unique in ${orgName}.`}>
					<Input
						value={current.name}
						placeholder="storefront"
						disabled={!canWrite}
						aria-invalid={shownErr !== null}
						className="h-9 max-w-[320px] text-[13.5px]"
						onChange={(e) => {
							setDraft({ ...current, name: e.target.value.toLowerCase() });
							setSaved(false);
							setServerNameErr(null);
						}}
					/>
					{shownErr !== null && <span className="text-sm text-bad-fg">{shownErr}</span>}
				</SettingsRow>
				<SettingsRow label="Description">
					<Textarea
						rows={3}
						maxLength={256}
						value={current.desc}
						placeholder="What runs in this workspace"
						disabled={!canWrite}
						className="text-[13.5px]"
						onChange={(e) => {
							setDraft({ ...current, desc: e.target.value });
							setSaved(false);
						}}
					/>
					<span className={cn("self-end text-sm", current.desc.length > 240 ? "text-warn-fg" : "text-fg4")}>
						{current.desc.length} / 256
					</span>
				</SettingsRow>
				<SettingsRow label="Created" last>
					<span className="text-fg2">{created}</span>
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

			<DangerCard
				title="Delete workspace"
				subtitle={subtitle}
				subtitleWarn={hasRes}
				label={`Delete ${ws.name}`}
				blockedReason={blockedReason}
				onClick={() => {
					setDeleteOpen(true);
				}}
			/>

			<ConfirmDeleteDialog
				open={deleteOpen}
				onOpenChange={setDeleteOpen}
				title={`Delete ${ws.name}`}
				text={`This removes ${ws.name}, its environments and everyone's access to it.`}
				target={ws.name}
				confirmLabel="Delete workspace"
				pending={remove.isPending}
				onConfirm={confirmDelete}
			/>
		</div>
	);
}

export function SettingsSkeleton() {
	return (
		<div className="flex flex-col gap-5">
			<SettingsCard>
				{[0, 1, 2].map((i) => (
					<SettingsRow key={i} label="" last={i === 2}>
						<Skeleton className="h-9 w-full max-w-[320px]" />
					</SettingsRow>
				))}
			</SettingsCard>
			<Skeleton className="h-[74px] w-full rounded-lg" />
		</div>
	);
}
