import { useMutation } from "@connectrpc/connect-query";
import { inviteOrgMember } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Invitation } from "@gen/loco/org/v1/org_pb";
import { CopyIcon, MailPlusIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { useCopy } from "@/hooks/useCopy";
import { getErrorMessage } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { statusBadge, statusDetail } from "./invitations";

type Outcome = { kind: "invited"; invitation: Invitation } | { kind: "failed"; message: string };

export function InviteDialog({
	orgId,
	open,
	onClose,
	onInvited,
}: {
	orgId: string;
	open: boolean;
	onClose: () => void;
	onInvited: () => void;
}) {
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
		>
			{open && <InviteForm orgId={orgId} onClose={onClose} onInvited={onInvited} />}
		</Dialog>
	);
}

function InviteForm({ orgId, onClose, onInvited }: { orgId: string; onClose: () => void; onInvited: () => void }) {
	const [email, setEmail] = useState("");
	const [outcome, setOutcome] = useState<Outcome | null>(null);
	const [copied, copy] = useCopy();
	const mutation = useMutation(inviteOrgMember);
	const trimmed = email.trim();

	const submit = async () => {
		if (trimmed === "" || mutation.isPending) return;
		try {
			const res = await mutation.mutateAsync({ orgId, email: trimmed });
			if (res.invitation !== undefined) setOutcome({ kind: "invited", invitation: res.invitation });
			setEmail("");
			onInvited();
		} catch (err) {
			setOutcome({
				kind: "failed",
				message: getErrorMessage(err, "Failed to send the invitation"),
			});
		}
	};

	const invitation = outcome?.kind === "invited" ? outcome.invitation : undefined;
	const tone = invitation === undefined ? undefined : statusBadge(invitation.status).tone;

	return (
		<DialogContent title="Invite a member" icon={<MailPlusIcon />}>
			<form
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					void submit();
				}}
			>
				<DialogBody>
					<Field label="Email">
						<Input
							type="email"
							autoFocus
							value={email}
							placeholder="teammate@example.com"
							onChange={(e) => {
								setEmail(e.target.value);
							}}
						/>
					</Field>
					<div
						aria-live="polite"
						className="flex h-16 items-start justify-between gap-3 rounded-sm border border-line bg-bg2 px-3 py-2.5"
					>
						<div className="flex min-w-0 flex-col gap-0.5">
							{outcome === null && (
								<span className="text-sm text-fg3">
									They join as a member the first time they sign in with this email.
								</span>
							)}
							{outcome?.kind === "failed" && (
								<span className="line-clamp-3 text-sm text-bad-fg">{outcome.message}</span>
							)}
							{invitation !== undefined && (
								<>
									<span className="truncate text-sm font-semibold">{invitation.email}</span>
									<span
										title={statusDetail(invitation)}
										className={cn(
											"line-clamp-2 text-sm",
											tone === "bad" ? "text-bad-fg" : tone === "ok" ? "text-ok-fg" : "text-fg2",
										)}
									>
										{statusDetail(invitation)}
									</span>
								</>
							)}
						</div>
						<Button
							type="button"
							variant="outline"
							size="sm"
							className={cn("min-w-[92px] shrink-0 justify-center", invitation === undefined && "invisible")}
							tabIndex={invitation === undefined ? -1 : 0}
							onClick={() => {
								if (invitation !== undefined) copy("link", invitation.link);
							}}
						>
							<CopyIcon />
							{copied === "link" ? "Copied" : "Copy link"}
						</Button>
					</div>
				</DialogBody>
				<DialogFooter>
					<Button type="button" variant="outline" className="px-3" onClick={onClose}>
						Close
					</Button>
					<Button type="submit" className="px-3" disabled={trimmed === "" || mutation.isPending}>
						{mutation.isPending ? "Sending…" : "Send invite"}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
