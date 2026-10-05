import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { toast } from "sonner";

import { deleteOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization } from "@gen/loco/org/v1/org_pb";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { toastConnectError } from "@/lib/error-handler";

export function DeleteOrgDialog({
	org,
	onOpenChange,
	onSuccess,
}: {
	org: Organization | null;
	onOpenChange: (open: boolean) => void;
	onSuccess: () => void;
}) {
	const [confirmName, setConfirmName] = useState("");
	const { mutate: mutateDeleteOrg, isPending } = useMutation(deleteOrg);
	const orgName = org?.name ?? "";
	const matches = org !== null && confirmName === orgName;

	const handleOpenChange = (next: boolean) => {
		if (isPending) {
			return;
		}
		if (!next) {
			setConfirmName("");
		}
		onOpenChange(next);
	};

	const handleDelete = () => {
		if (org === null || !matches) {
			return;
		}
		mutateDeleteOrg(
			{ orgId: org.id },
			{
				onSuccess: () => {
					toast.success(`Organization "${orgName}" deleted`);
					setConfirmName("");
					onOpenChange(false);
					onSuccess();
				},
				onError: (error) => {
					toastConnectError(error, "Failed to delete organization");
				},
			},
		);
	};

	return (
		<Dialog open={org !== null} onOpenChange={handleOpenChange}>
			<DialogContent title="Delete organization" className="w-[480px]">
				<DialogBody>
					<span className="leading-normal">
						Deleting <span className="font-semibold">{orgName}</span> permanently deletes all workspaces in this
						organization, all resources and deployments, and all configuration and data. This action cannot be
						undone.
					</span>
					<label className="flex flex-col gap-1.5">
						<span className="text-[12.5px] text-fg2">
							Type <span className="font-semibold text-foreground">{orgName}</span> to confirm
						</span>
						<Input
							className="h-9"
							value={confirmName}
							onChange={(e) => {
								setConfirmName(e.target.value);
							}}
							disabled={isPending}
							autoFocus
						/>
					</label>
				</DialogBody>
				<DialogFooter>
					<Button
						variant="outline"
						size="lg"
						onClick={() => {
							handleOpenChange(false);
						}}
						disabled={isPending}
					>
						Cancel
					</Button>
					<Button variant="destructive" size="lg" onClick={handleDelete} disabled={isPending || !matches}>
						{isPending ? "Deleting..." : "Delete organization"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
