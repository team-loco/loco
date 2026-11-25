import { useMutation } from "@connectrpc/connect-query";
import { resumeOrg, suspendOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization } from "@gen/loco/org/v1/org_pb";
import { useQueryClient } from "@tanstack/react-query";
import { PauseIcon, PlayIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { InlineCode } from "@/components/design/InlineCode";
import { toastConnectError } from "@/lib/error-handler";
import { toast } from "@/lib/toast";

import { ReasonDialog } from "./ReasonDialog";
import { refreshOrgs } from "./shared";

/** The Suspend or Resume button of an organization, with its confirm step. */
export function OrgSuspensionButton({ org }: { org: Organization }) {
	const queryClient = useQueryClient();
	const suspend = useMutation(suspendOrg);
	const resume = useMutation(resumeOrg);
	const [open, setOpen] = useState(false);
	const suspended = org.suspendedAt !== undefined;
	const close = () => {
		setOpen(false);
	};

	const run = async (action: () => Promise<unknown>, done: string, failed: string) => {
		try {
			await action();
			close();
			toast(done);
			await refreshOrgs(queryClient);
		} catch (err) {
			toastConnectError(err, failed);
		}
	};

	return (
		<>
			<Button
				variant={suspended ? "outline" : "destructive-outline"}
				size="sm"
				onClick={() => {
					setOpen(true);
				}}
			>
				{suspended ? <PlayIcon /> : <PauseIcon />}
				{suspended ? "Resume" : "Suspend"}
			</Button>
			{suspended ? (
				<ConfirmDialog
					open={open}
					title={`Resume ${org.name}`}
					description={
						<>
							Resuming <InlineCode>{org.name}</InlineCode> sends every active deployment back to its cluster, with the
							secret and domain changes made during the suspension. Deploys work again.
						</>
					}
					confirmLabel="Resume organization"
					confirmVariant="default"
					pending={resume.isPending}
					onConfirm={() => {
						void run(
							async () => await resume.mutateAsync({ orgId: org.id }),
							`Resumed ${org.name}`,
							`Failed to resume ${org.name}`,
						);
					}}
					onClose={close}
				/>
			) : (
				<ReasonDialog
					open={open}
					title={`Suspend ${org.name}`}
					description="Every application of the organization is removed from its cluster, so none of its pods run, and its queued and running builds are canceled. Deploys, scaling and new builds fail until it is resumed. Services, settings, secrets, domains and deployment history are kept, and members can still read them."
					reasonHint="One line. Members see it in a banner, and it goes in the organization's audit log."
					confirmLabel="Suspend organization"
					confirmVariant="destructive"
					pending={suspend.isPending}
					onSubmit={(reason) => {
						void run(
							async () => await suspend.mutateAsync({ orgId: org.id, reason }),
							`Suspended ${org.name}`,
							`Failed to suspend ${org.name}`,
						);
					}}
					onClose={close}
				/>
			)}
		</>
	);
}
