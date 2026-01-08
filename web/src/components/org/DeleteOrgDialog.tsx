import { useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { deleteOrg } from "@/gen/org/v1";
import { useMutation } from "@connectrpc/connect-query";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/error-handler";
import Loader from "@/assets/loader.svg?react";
import { AlertTriangle } from "lucide-react";

interface DeleteOrgDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	orgId: bigint;
	orgName: string;
	onSuccess?: () => void;
}

export function DeleteOrgDialog({
	open,
	onOpenChange,
	orgId,
	orgName,
	onSuccess,
}: DeleteOrgDialogProps) {
	const navigate = useNavigate();
	const [confirmName, setConfirmName] = useState("");

	const { mutate: mutateDeleteOrg, isPending } = useMutation(deleteOrg);

	const handleDelete = () => {
		if (confirmName !== orgName) {
			toast.error("Organization name does not match");
			return;
		}

		mutateDeleteOrg(
			{ orgId },
			{
				onSuccess: () => {
					toast.success(`Organization "${orgName}" deleted`);
					setConfirmName("");
					onOpenChange(false);

					// Call success callback if provided, otherwise navigate to dashboard
					if (onSuccess) {
						onSuccess();
					} else {
						// Navigate to dashboard without org param (will default to first org)
						navigate("/dashboard");
					}
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to delete organization"));
				},
			}
		);
	};

	const handleClose = () => {
		if (!isPending) {
			setConfirmName("");
			onOpenChange(false);
		}
	};

	return (
		<Dialog open={open} onOpenChange={handleClose}>
			<DialogContent className="sm:max-w-[500px]">
				<DialogHeader>
					<div className="flex items-center gap-2">
						<div className="flex size-10 items-center justify-center rounded-full bg-red-100 dark:bg-red-900/20">
							<AlertTriangle className="size-5 text-red-600 dark:text-red-500" />
						</div>
						<div>
							<DialogTitle>Delete Organization</DialogTitle>
							<DialogDescription className="mt-1">
								This action cannot be undone
							</DialogDescription>
						</div>
					</div>
				</DialogHeader>

				<div className="space-y-4 py-4">
					<div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-900/50 dark:bg-red-900/20">
						<p className="text-sm text-red-800 dark:text-red-200">
							Deleting <span className="font-semibold">{orgName}</span> will
							permanently delete:
						</p>
						<ul className="mt-2 list-inside list-disc space-y-1 text-sm text-red-700 dark:text-red-300">
							<li>All workspaces in this organization</li>
							<li>All resources and deployments</li>
							<li>All configuration and data</li>
						</ul>
					</div>

					<div className="grid gap-2">
						<Label htmlFor="confirm-name">
							Type <span className="font-mono font-semibold">{orgName}</span>{" "}
							to confirm
						</Label>
						<Input
							id="confirm-name"
							placeholder={orgName}
							value={confirmName}
							onChange={(e) => setConfirmName(e.target.value)}
							disabled={isPending}
							autoFocus
						/>
					</div>
				</div>

				<DialogFooter>
					<Button
						type="button"
						variant="secondary"
						onClick={handleClose}
						disabled={isPending}
					>
						Cancel
					</Button>
					<Button
						type="button"
						variant="destructive"
						onClick={handleDelete}
						disabled={isPending || confirmName !== orgName}
					>
						{isPending ? (
							<>
								<Loader className="w-4 h-4 mr-2" />
								Deleting...
							</>
						) : (
							"Delete Organization"
						)}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
