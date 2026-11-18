import { create } from "@bufbuild/protobuf";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { deleteUser, getAccountDeletionImpact } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { User } from "@gen/loco/user/v1/user_pb";
import { TokenSchema, type Token } from "@gen/loco/token/v1/token_pb";
import { keepPreviousData, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";

import { useAuth } from "@/auth/AuthProvider";
import { Button } from "@/components/design/Button";
import { ConfirmDeleteDialog } from "@/components/design/ConfirmDeleteDialog";
import { DangerZone } from "@/components/design/DangerZone";
import { Pager } from "@/components/design/Pager";
import { Spinner } from "@/components/design/Spinner";
import { getErrorMessage } from "@/lib/error-handler";
import { RevokeDialog } from "@/pages/tokens/RevokeDialog";

import { SettingsCard } from "./parts";

const SURVIVING_TOKENS =
	"Organization, workspace and resource tokens you issued keep working after you delete your account. Revoke unused tokens or arrange a handover with their administrators.";
const TOKEN_PAGE_SIZE = 10;

export function DeleteAccount({ user }: { user: User }) {
	const { logout } = useAuth();
	const queryClient = useQueryClient();
	const [pages, setPages] = useState([""]);
	const impact = useQuery(
		getAccountDeletionImpact,
		{ pageSize: TOKEN_PAGE_SIZE, pageToken: pages.at(-1) ?? "" },
		{ placeholderData: keepPreviousData },
	);
	const remove = useMutation(deleteUser);
	const [deleteOpen, setDeleteOpen] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [revoke, setRevoke] = useState<Token | null>(null);
	const [now] = useState(Date.now);
	const blockers = impact.data?.lastActiveAdminOrganizations ?? [];
	const blockedReason = impact.isPending
		? "Checking account deletion"
		: impact.isError
			? "Retry the account check before deleting"
			: blockers.length > 0
				? "Assign another active administrator to each organization below"
				: null;

	const refresh = () => {
		setPages([""]);
		void queryClient.invalidateQueries();
	};
	const confirmDelete = () => {
		if (remove.isPending || blockedReason !== null) return;
		setError(null);
		remove.mutate(
			{ userId: user.id },
			{
				onSuccess: () => {
					toast.success("Account deleted");
					setDeleteOpen(false);
					void logout("/login");
				},
				onError: (err) => {
					setError(getErrorMessage(err, "Failed to delete account"));
					setDeleteOpen(false);
					refresh();
				},
			},
		);
	};

	return (
		<>
			<DangerZone
				title="Delete your account"
				description="Permanently deletes your profile, sessions and personal tokens, removes your memberships and signs you out. Your organizations, workspaces and their history stay."
				actionLabel="Delete account"
				blockedReason={blockedReason}
				onAction={() => {
					setError(null);
					setDeleteOpen(true);
				}}
			/>
			<SettingsCard className="flex flex-col gap-3 p-4">
				{error !== null && (
					<p role="alert" className="text-sm text-bad-fg">
						{error}
					</p>
				)}
				{impact.isPending ? (
					<p role="status" className="flex items-center gap-2 text-sm text-fg3">
						<Spinner className="size-4" />
						Checking account deletion…
					</p>
				) : impact.isError ? (
					<div role="alert" className="flex flex-wrap items-center gap-3 text-sm text-bad-fg">
						<span>{getErrorMessage(impact.error, "Failed to check account deletion")}</span>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								void impact.refetch();
							}}
						>
							Retry
						</Button>
					</div>
				) : (
					<>
						{blockers.length > 0 && (
							<div className="flex flex-col gap-2">
								<p className="text-sm font-medium">These organizations need another active administrator</p>
								<p className="text-sm text-fg3">
									Assign another active member as an administrator on the Team page, then check again.
								</p>
								<ul className="list-none divide-y divide-line">
									{blockers.map((org) => (
										<li key={org.orgId} className="py-2">
											<Link className="text-sm underline" to={`/org/${org.orgId}/team`}>
												{org.orgName}
											</Link>
										</li>
									))}
								</ul>
								{impact.data.hasMoreBlockingOrganizations && (
									<p className="text-sm text-warn-fg">
										More organizations need another active administrator. Resolve these, then check again to review the
										rest.
									</p>
								)}
							</div>
						)}
						<p className="text-sm font-medium">Automation tokens</p>
						<p className="text-sm text-fg3">{SURVIVING_TOKENS}</p>
						{impact.data.hasInaccessibleOwnedTokens && (
							<p className="text-sm text-warn-fg">
								You also issued tokens for owners you can no longer access. Their administrators can review and revoke
								them.
							</p>
						)}
						{impact.data.ownedTokens.length === 0 ? (
							<p className="text-sm text-fg3">No active tokens to show on this page.</p>
						) : (
							<ul aria-label="Surviving automation tokens" className="list-none divide-y divide-line">
								{impact.data.ownedTokens.map((entry) => (
									<li
										key={`${entry.entityType}:${entry.entityId}:${entry.name}`}
										className="flex flex-wrap items-center justify-between gap-3 py-3"
									>
										<div className="min-w-0 flex-1 basis-40">
											<p className="break-all font-mono text-sm">{entry.name}</p>
											<p className="break-words text-xs text-fg3">{entry.ownerName}</p>
										</div>
										{entry.canRevoke ? (
											<Button
												variant="destructive-outline"
												size="sm"
												onClick={() => {
													setRevoke(
														create(TokenSchema, {
															name: entry.name,
															entityType: entry.entityType,
															entityId: entry.entityId,
															expiresAt: entry.expiresAt,
															displaySuffix: entry.displaySuffix,
														}),
													);
												}}
											>
												Revoke
											</Button>
										) : (
											<span className="text-xs text-fg3">An owner administrator can revoke</span>
										)}
									</li>
								))}
							</ul>
						)}
						<div className="flex flex-wrap items-center gap-2" aria-busy={impact.isFetching}>
							<Pager
								label={`Page ${pages.length.toString()}`}
								total={impact.data.ownedTokens.length}
								pageSizes={[TOKEN_PAGE_SIZE]}
								onPrev={
									pages.length === 1
										? null
										: () => {
												if (!impact.isFetching) setPages((current) => current.slice(0, -1));
											}
								}
								onNext={
									!impact.data.nextPageToken
										? null
										: () => {
												if (!impact.isFetching) setPages((current) => [...current, impact.data.nextPageToken]);
											}
								}
								className="flex-1 border-t-0 p-0"
							/>
							<Button variant="ghost" size="sm" disabled={impact.isFetching} onClick={refresh}>
								Check again
							</Button>
						</div>
					</>
				)}
			</SettingsCard>
			<ConfirmDeleteDialog
				open={deleteOpen}
				onOpenChange={setDeleteOpen}
				title="Delete your account"
				target={user.email}
				confirmLabel="Delete account"
				text="This permanently deletes your profile, sessions and personal tokens, removes your memberships and signs you out. Organizations, workspaces and their history stay."
				pending={remove.isPending}
				onConfirm={confirmDelete}
			>
				<p className="text-sm text-warn-fg">{SURVIVING_TOKENS}</p>
			</ConfirmDeleteDialog>
			<RevokeDialog
				token={revoke}
				now={now}
				onClose={() => {
					setRevoke(null);
				}}
				onRevoked={(name) => {
					setRevoke(null);
					toast.success(`Revoked ${name}`);
					refresh();
				}}
			/>
		</>
	);
}
