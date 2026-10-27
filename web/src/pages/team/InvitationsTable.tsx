import { useMutation } from "@connectrpc/connect-query";
import { resendInvitation, revokeInvitation } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Invitation } from "@gen/loco/org/v1/org_pb";
import { CopyIcon, RotateCwIcon, XIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Skeleton } from "@/components/design/Skeleton";
import { useCopy } from "@/hooks/useCopy";
import { toastConnectError } from "@/lib/error-handler";
import { formatMonthDayYear, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { statusBadge, statusDetail } from "./invitations";

const COLS = "grid-cols-1 md:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_112px_88px_284px]";

export function InvitationsTable({
	invitations,
	isLoading,
	canManage,
	onChanged,
}: {
	invitations: Invitation[];
	isLoading: boolean;
	canManage: boolean;
	onChanged: () => void;
}) {
	const resend = useMutation(resendInvitation);
	const revoke = useMutation(revokeInvitation);
	const [copied, copy] = useCopy();
	const busy = resend.isPending || revoke.isPending;

	const doResend = async (invitation: Invitation) => {
		try {
			const res = await resend.mutateAsync({ id: invitation.id });
			onChanged();
			toast(res.invitation === undefined ? "Invitation resent" : statusDetail(res.invitation));
		} catch (err) {
			toastConnectError(err, "Failed to resend the invitation");
		}
	};

	const doRevoke = async (invitation: Invitation) => {
		try {
			await revoke.mutateAsync({ id: invitation.id });
			onChanged();
			toast(`Revoked the invitation for ${invitation.email}`);
		} catch (err) {
			toastConnectError(err, "Failed to revoke the invitation");
		}
	};

	return (
		<section
			aria-label="Pending invitations"
			className="min-w-0 overflow-hidden rounded-lg border border-line bg-background"
		>
			<div
				className={cn(
					"grid h-9 items-center gap-4 border-b border-line bg-bg2 px-4 text-sm font-semibold text-fg2",
					COLS,
				)}
			>
				<span>Email</span>
				<span className="hidden md:inline">Invited by</span>
				<span className="hidden md:inline">Expires</span>
				<span className="hidden md:inline">Status</span>
				<span className="hidden md:inline" />
			</div>
			{isLoading && (
				<div className={cn("grid items-center gap-4 px-4 py-2.5", COLS)}>
					<Skeleton className="h-4 w-48" />
					<Skeleton className="hidden h-4 w-28 md:block" />
					<Skeleton className="hidden h-4 w-20 md:block" />
					<Skeleton className="hidden h-5 w-[88px] md:block" />
					<span />
				</div>
			)}
			{!isLoading && invitations.length === 0 && <div className="px-4 py-3.5 text-fg3">No pending invitations</div>}
			{!isLoading &&
				invitations.map((invitation) => {
					const badge = statusBadge(invitation.status);
					const invitedBy = invitation.invitedBy;
					return (
						<div
							key={invitation.id}
							className={cn("grid items-center gap-x-4 gap-y-1 border-b border-line px-4 py-2.5 last:border-b-0", COLS)}
						>
							<span className="truncate font-semibold">{invitation.email}</span>
							<span className="truncate text-fg2">
								{invitedBy === undefined ? "" : invitedBy.name || invitedBy.email}
							</span>
							<span className="text-fg2">{formatMonthDayYear(tsMs(invitation.expiresAt))}</span>
							<Badge tone={badge.tone} title={statusDetail(invitation)} className="w-[88px] justify-center">
								{badge.label}
							</Badge>
							<div className="flex items-center justify-end gap-1.5">
								<Button
									variant="outline"
									size="sm"
									className="w-[96px] justify-center"
									onClick={() => {
										copy(invitation.id, invitation.link);
									}}
								>
									<CopyIcon />
									{copied === invitation.id ? "Copied" : "Copy link"}
								</Button>
								<Button
									variant="outline"
									size="sm"
									className="w-[76px] justify-center"
									disabled={!canManage || busy}
									title={canManage ? undefined : "Only organization admins can resend invitations"}
									onClick={() => {
										void doResend(invitation);
									}}
								>
									<RotateCwIcon />
									Resend
								</Button>
								<Button
									variant="destructive-outline"
									size="sm"
									className="w-[76px] justify-center"
									disabled={!canManage || busy}
									title={canManage ? undefined : "Only organization admins can revoke invitations"}
									onClick={() => {
										void doRevoke(invitation);
									}}
								>
									<XIcon />
									Revoke
								</Button>
							</div>
						</div>
					);
				})}
		</section>
	);
}
