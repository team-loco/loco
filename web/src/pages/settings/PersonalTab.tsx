import { createQueryOptions, useMutation, useTransport } from "@connectrpc/connect-query";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { deleteUser, updateUser } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { User } from "@gen/loco/user/v1/user_pb";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { Building2Icon, ChevronRightIcon, KeyRoundIcon, LayersIcon, LogOutIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { toast } from "sonner";

import { useAuth } from "@/auth/AuthProvider";
import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { DangerZone } from "@/components/design/DangerZone";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { SaveBar } from "@/components/design/SaveBar";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useMyScopes } from "@/hooks/useMyScopes";
import { toastConnectError } from "@/lib/error-handler";
import { settingsPath } from "@/lib/routes";
import { userLabel } from "@/lib/user";
import { cn } from "@/lib/utils";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { accountBlockers } from "./account";
import { ConfirmDeleteDialog } from "./ConfirmDeleteDialog";
import { formatDay, KeyValues, SettingsCard, SettingsSection } from "./parts";

const TOKENS_PATH = "/tokens?owner=personal";

export function PersonalTab({
	user,
	draft,
	onDraft,
}: {
	user: User;
	draft: string | null;
	onDraft: (name: string | null) => void;
}) {
	const { logout } = useAuth();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const update = useMutation(updateUser);
	const remove = useMutation(deleteUser);
	const [deleteOpen, setDeleteOpen] = useState(false);

	const name = draft ?? user.name;
	const nameT = name.trim();
	const dirty = name !== user.name;
	const nameErr = nameT === "" ? "Name required" : null;
	const shownErr = dirty ? nameErr : null;

	const save = () => {
		if (nameErr !== null || update.isPending) return;
		update.mutate(
			{ userId: user.id, name: nameT },
			{
				onSuccess: () => {
					onDraft(null);
					toast.success("Saved");
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to update your name");
				},
			},
		);
	};

	const confirmDelete = () => {
		remove.mutate(
			{ userId: user.id },
			{
				onSuccess: () => {
					toast.success("Account deleted");
					setDeleteOpen(false);
					void logout();
					void navigate("/login", { replace: true });
				},
				onError: (err) => {
					toastConnectError(err, "Failed to delete account");
				},
			},
		);
	};

	return (
		<div className="flex flex-col gap-[22px]">
			<SettingsSection title="Profile">
				<SettingsCard className="flex flex-col gap-4 px-5 py-[18px]">
					<div className="flex min-w-0 items-center gap-3.5">
						<MemberAvatar name={user.name} email={user.email} avatarUrl={user.avatarUrl} className="size-12" />
						<div className="flex min-w-0 flex-col gap-0.5">
							<span className="truncate text-[15px] font-medium">{userLabel(user)}</span>
							<span className="truncate font-mono text-[12px] text-fg3">{user.email}</span>
						</div>
					</div>
					<Field label="Name" error={shownErr}>
						<Input
							value={name}
							autoComplete="name"
							aria-invalid={shownErr !== null}
							className="h-9 max-w-[420px] text-[13.5px]"
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
									value={user.id}
									className="h-6 max-w-full bg-panel2 px-2 font-mono text-[12px] font-normal"
								>
									<span className="truncate">{user.id}</span>
								</CopyId>,
							],
							["Joined", formatDay(user.createdAt)],
						]}
					/>
				</SettingsCard>
			</SettingsSection>

			<SettingsSection title="Access">
				<SettingsCard>
					<Link
						to={TOKENS_PATH}
						className="flex items-center gap-3 border-b border-line px-5 py-3.5 text-foreground no-underline hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none focus-visible:ring-inset"
					>
						<KeyRoundIcon className="size-4 shrink-0 text-fg3" />
						<span className="flex min-w-0 flex-1 flex-col gap-0.5">
							<span className="text-[13.5px] font-medium">Personal access tokens</span>
							<span className="text-[12.5px] text-fg3">Tokens that act as you, for the CLI and scripts.</span>
						</span>
						<ChevronRightIcon className="size-3.5 shrink-0 text-fg3" />
					</Link>
					<div className="flex flex-wrap items-center gap-3 px-5 py-3.5">
						<LogOutIcon className="size-4 shrink-0 text-fg3" />
						<span className="min-w-0 flex-1 text-[13.5px] font-medium">Log out</span>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								void logout();
							}}
						>
							Log out
						</Button>
					</div>
				</SettingsCard>
			</SettingsSection>

			<SettingsSection title="Danger zone">
				<DeleteAccount
					userId={user.id}
					onDelete={() => {
						setDeleteOpen(true);
					}}
				/>
			</SettingsSection>

			{dirty && (
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
				title="Delete your account"
				text="This permanently deletes your user and your organization memberships, and signs you out."
				target={user.email}
				confirmLabel="Delete account"
				pending={remove.isPending}
				onConfirm={confirmDelete}
			/>
		</div>
	);
}

function DeleteAccount({ userId, onDelete }: { userId: string; onDelete: () => void }) {
	const transport = useTransport();
	const { orgs, workspacesSettled } = useOrgWorkspace();
	const scopes = useMyScopes();
	const blockers = accountBlockers(userId, orgs, scopes.scopes);
	const workspaceResults = useQueries({
		queries: blockers.workspaceIds.map((workspaceId) =>
			createQueryOptions(getWorkspace, { workspaceId }, { transport }),
		),
	});

	const ownsOrgs = blockers.orgs.length > 0;
	const hasWorkspaces = blockers.workspaceIds.length > 0;
	const blocked = ownsOrgs || hasWorkspaces;
	const blockedReason =
		scopes.isLoading || !workspacesSettled
			? "Checking your memberships"
			: ownsOrgs && hasWorkspaces
				? "Delete your organizations and leave your workspaces first"
				: ownsOrgs
					? "Delete the organizations you created first"
					: hasWorkspaces
						? "Leave your workspaces first"
						: null;
	const description = blocked
		? "Your account can be deleted once you no longer own an organization or have access to a workspace."
		: "Permanently deletes your user and signs you out.";

	return (
		<>
			<DangerZone
				title="Delete your account"
				description={description}
				warn={blocked}
				actionLabel="Delete account"
				blockedReason={blockedReason}
				onAction={onDelete}
			/>
			{blocked && (
				<SettingsCard>
					<ul className="m-0 list-none p-0">
						{blockers.orgs.map((o) => (
							<BlockerRow
								key={o.id}
								to={`/org/${o.id}/settings`}
								icon={<Building2Icon />}
								name={o.name}
								detail="Organization you created. Delete it in its settings."
							/>
						))}
						{blockers.workspaceIds.map((id, i) => {
							const ws = workspaceResults[i]?.data?.workspace;
							return (
								<BlockerRow
									key={id}
									to={ws ? settingsPath(ws.orgId, ws.id, "workspace") : null}
									icon={<LayersIcon />}
									name={ws ? ws.name : id}
									detail={
										ws
											? `Workspace in ${ws.orgName}. Delete it, or remove your access on the Team page.`
											: "Workspace you have access to."
									}
								/>
							);
						})}
					</ul>
				</SettingsCard>
			)}
		</>
	);
}

function BlockerRow({ to, icon, name, detail }: { to: string | null; icon: ReactNode; name: string; detail: string }) {
	const body = (
		<>
			<span className="flex shrink-0 text-fg3 [&_svg]:size-4">{icon}</span>
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className="truncate font-mono text-[13px]">{name}</span>
				<span className="text-[12.5px] text-fg3">{detail}</span>
			</span>
			{to !== null && <ChevronRightIcon className="size-3.5 shrink-0 text-fg3" />}
		</>
	);
	const rowClass = "flex items-center gap-3 px-5 py-3";
	return (
		<li className="border-b border-line last:border-b-0">
			{to === null ? (
				<div className={rowClass}>{body}</div>
			) : (
				<Link
					to={to}
					className={cn(
						rowClass,
						"text-foreground no-underline hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none focus-visible:ring-inset",
					)}
				>
					{body}
				</Link>
			)}
		</li>
	);
}
