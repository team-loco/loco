import { useMutation, useQuery, useTransport } from "@connectrpc/connect-query";
import type { User } from "@gen/loco/org/v1/org_pb";
import {
	deleteWorkspace,
	getWorkspace,
	updateWorkspace,
} from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQuery as useTanstackQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { ConfirmDeleteDialog } from "@/components/design/ConfirmDeleteDialog";
import { CopyId } from "@/components/design/CopyId";
import { DangerZone } from "@/components/design/DangerZone";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { SaveBar } from "@/components/design/SaveBar";
import { Skeleton } from "@/components/design/Skeleton";
import { Textarea } from "@/components/design/Textarea";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import type { AccessLevel } from "@/hooks/useMyScopes";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { pluralize } from "@/lib/format";
import { workspacePath } from "@/lib/routes";
import { userLabel } from "@/lib/user";

import { ActivityPanel } from "./ActivityPanel";
import { EnvironmentsSection } from "./EnvironmentsSection";
import { formatDay, KeyValues, SettingsCard, SettingsSection } from "./parts";
import { WebhooksSection } from "./WebhooksSection";
import type { ResourceCount } from "./useResourceCounts";

const MAX_NAME = 63;
const MAX_DESCRIPTION = 256;

export interface WorkspaceDraft {
	name: string;
	desc: string;
}

export function WorkspaceTab({
	orgId,
	workspaceId,
	workspaces,
	users,
	level,
	resourceCount,
	draft,
	onDraft,
}: {
	orgId: string;
	workspaceId: string;
	workspaces: Workspace[];
	users: User[];
	level: AccessLevel;
	resourceCount: ResourceCount | undefined;
	draft: WorkspaceDraft | null;
	onDraft: (draft: WorkspaceDraft | null) => void;
}) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const transport = useTransport();
	const { data, isLoading, error } = useQuery(getWorkspace, { workspaceId });
	const { data: membersRes } = useTanstackQuery(workspaceMembersOptions(transport, workspaceId));
	const update = useMutation(updateWorkspace);
	const remove = useMutation(deleteWorkspace);

	const [deleteOpen, setDeleteOpen] = useState(false);

	const ws = data?.workspace;
	if (isLoading) return <SettingsSkeleton />;
	if (!ws) {
		const message = getErrorMessage(error, "Failed to load workspace");
		return <div className="rounded-lg border border-line px-5 py-8 text-fg3">{message}</div>;
	}

	const current: WorkspaceDraft = draft ?? { name: ws.name, desc: ws.description };
	const nameT = current.name.trim();
	const dirty = current.name !== ws.name || current.desc !== ws.description;
	const nameErr =
		nameT === "" ? "Name required" : nameT.length > MAX_NAME ? `Max ${MAX_NAME.toString()} characters` : null;
	const canWrite = level >= 2;
	const canAdmin = level >= 3;
	const shownErr = dirty ? nameErr : null;
	const creator = users.find((u) => u.id === ws.createdBy);
	const createdDay = formatDay(ws.createdAt);
	const created = creator ? `${createdDay} by ${userLabel(creator)}` : createdDay;

	const nRes = resourceCount?.count ?? 0;
	const hasRes = nRes > 0 || resourceCount?.more === true;
	const members = membersRes?.members.length ?? 0;
	const resLabel = resourceCount === undefined ? "—" : `${nRes.toString()}${resourceCount.more ? "+" : ""}`;
	const subtitle = hasRes
		? `Delete the ${resLabel} ${nRes === 1 ? "resource" : "resources"} in ${ws.name} first.`
		: `${resLabel} resources, ${pluralize(members, "member")} with direct access`;
	const blockedReason = !canAdmin
		? `Requires Admin on ${ws.name}`
		: resourceCount === undefined
			? "Checking for resources"
			: hasRes
				? "Workspace still has resources"
				: null;

	const save = () => {
		if (nameErr !== null || update.isPending) return;
		update.mutate(
			{ workspaceId: ws.id, name: nameT, description: current.desc },
			{
				onSuccess: () => {
					onDraft(null);
					toast.success("Saved");
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
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
		<div className="flex flex-col gap-[22px]">
			<SettingsSection title="General" meta={ws.name}>
				<SettingsCard className="flex flex-col gap-4 px-5 py-[18px]">
					<Field label="Name" error={shownErr}>
						<Input
							value={current.name}
							placeholder="storefront"
							disabled={!canWrite}
							aria-invalid={shownErr !== null}
							className="h-9 max-w-[420px] font-mono text-[13.5px]"
							onChange={(e) => {
								onDraft({ ...current, name: e.target.value.toLowerCase() });
							}}
						/>
					</Field>
					<Field label="Description" hint={`${current.desc.length.toString()} / ${MAX_DESCRIPTION.toString()}`}>
						<Textarea
							rows={2}
							maxLength={MAX_DESCRIPTION}
							value={current.desc}
							placeholder="What runs in this workspace"
							disabled={!canWrite}
							className="text-[13.5px]"
							onChange={(e) => {
								onDraft({ ...current, desc: e.target.value });
							}}
						/>
					</Field>
					<KeyValues
						items={[
							[
								"ID",
								<CopyId
									key="id"
									value={ws.id}
									className="h-6 max-w-full bg-panel2 px-2 font-mono text-[12px] font-normal"
								>
									<span className="truncate">{ws.id}</span>
								</CopyId>,
							],
							[
								"Organization",
								<span key="org" className="font-mono">
									{ws.orgName}
								</span>,
							],
							["Created", created],
						]}
					/>
				</SettingsCard>
			</SettingsSection>

			<EnvironmentsSection workspaceId={ws.id} workspaceName={ws.name} canWrite={canWrite} canAdmin={canAdmin} />

			{canAdmin && <WebhooksSection workspaceId={ws.id} workspaceName={ws.name} />}

			{canAdmin && (
				<ActivityPanel
					key={ws.id}
					orgId={orgId}
					workspaceId={ws.id}
					description={`Changes made to ${ws.name} and its resources, newest first.`}
					names={new Map([[ws.id, ws.name]])}
				/>
			)}

			<SettingsSection title="Danger zone">
				<DangerZone
					title={`Delete ${ws.name}`}
					description={subtitle}
					warn={hasRes}
					actionLabel="Delete workspace"
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
		<div className="flex max-w-[960px] flex-col gap-[22px]">
			<Skeleton className="h-[220px] w-full rounded-card" />
			<Skeleton className="h-[74px] w-full rounded-card" />
		</div>
	);
}
