import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
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
import { deleteWorkspace } from "@/gen/workspace/v1";
import { useMutation } from "@connectrpc/connect-query";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/error-handler";
import Loader from "@/assets/loader.svg?react";
import { AlertTriangle } from "lucide-react";

interface DeleteWorkspaceDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	workspaceId: bigint;
	workspaceName: string;
	onSuccess?: () => void;
}

export function DeleteWorkspaceDialog({
	open,
	onOpenChange,
	workspaceId,
	workspaceName,
	onSuccess,
}: DeleteWorkspaceDialogProps) {
	const navigate = useNavigate();
	const [searchParams] = useSearchParams();
	const [confirmName, setConfirmName] = useState("");

	const { mutate: mutateDeleteWorkspace, isPending } =
		useMutation(deleteWorkspace);

	const handleDelete = () => {
		if (confirmName !== workspaceName) {
			toast.error("Workspace name does not match");
			return;
		}

		mutateDeleteWorkspace(
			{ workspaceId },
			{
				onSuccess: () => {
					toast.success(`Workspace "${workspaceName}" deleted`);
					setConfirmName("");
					onOpenChange(false);

					// Call success callback if provided, otherwise navigate to dashboard
					if (onSuccess) {
						onSuccess();
					} else {
						// Navigate to dashboard, preserving org context
						const orgParam = searchParams.get("org");
						const url = orgParam ? `/dashboard?org=${orgParam}` : "/dashboard";
						navigate(url);
					}
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to delete workspace"));
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
							<DialogTitle>Delete Workspace</DialogTitle>
							<DialogDescription className="mt-1">
								This action cannot be undone
							</DialogDescription>
						</div>
					</div>
				</DialogHeader>

				<div className="space-y-4 py-4">
					<div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-900/50 dark:bg-red-900/20">
						<p className="text-sm text-red-800 dark:text-red-200">
							Deleting <span className="font-semibold">{workspaceName}</span>{" "}
							will permanently delete:
						</p>
						<ul className="mt-2 list-inside list-disc space-y-1 text-sm text-red-700 dark:text-red-300">
							<li>All resources and deployments</li>
							<li>All configuration and environment variables</li>
							<li>All data and logs</li>
						</ul>
					</div>

					<div className="grid gap-2">
						<Label htmlFor="confirm-name">
							Type{" "}
							<span className="font-mono font-semibold">{workspaceName}</span>{" "}
							to confirm
						</Label>
						<Input
							id="confirm-name"
							placeholder={workspaceName}
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
						disabled={isPending || confirmName !== workspaceName}
					>
						{isPending ? (
							<>
								<Loader className="w-4 h-4 mr-2" />
								Deleting...
							</>
						) : (
							"Delete Workspace"
						)}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
