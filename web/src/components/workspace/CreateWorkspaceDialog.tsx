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
import { Textarea } from "@/components/ui/textarea";
import { createWorkspace } from "@/gen/loco/workspace/v1";
import { useMutation } from "@connectrpc/connect-query";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/error-handler";
import Loader from "@/assets/loader.svg?react";

interface CreateWorkspaceDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	orgId: bigint;
	onSuccess?: (workspaceId: bigint) => void;
}

export function CreateWorkspaceDialog({
	open,
	onOpenChange,
	orgId,
	onSuccess,
}: CreateWorkspaceDialogProps) {
	const navigate = useNavigate();
	const [searchParams] = useSearchParams();
	const [workspaceName, setWorkspaceName] = useState("");
	const [description, setDescription] = useState("");

	const { mutate: mutateCreateWorkspace, isPending } =
		useMutation(createWorkspace);

	const handleSubmit = (e: React.FormEvent) => {
		e.preventDefault();

		if (!workspaceName.trim()) {
			toast.error("Workspace name is required");
			return;
		}

		mutateCreateWorkspace(
			{
				orgId,
				name: workspaceName.trim(),
				description: description.trim() || undefined,
			},
			{
				onSuccess: (response) => {
					const newWorkspaceId = response.workspaceId;
					if (newWorkspaceId) {
						toast.success(`Workspace "${workspaceName}" created`);
						setWorkspaceName("");
						setDescription("");
						onOpenChange(false);

						// Call success callback if provided, otherwise navigate
						if (onSuccess) {
							onSuccess(newWorkspaceId);
						} else {
							// Switch to new workspace and navigate to dashboard
							const orgParam = searchParams.get("org");
							const url = orgParam
								? `/dashboard?org=${orgParam}&workspace=${newWorkspaceId}`
								: `/dashboard?workspace=${newWorkspaceId}`;
							navigate(url);
						}
					}
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to create workspace"));
				},
			}
		);
	};

	const handleClose = () => {
		if (!isPending) {
			setWorkspaceName("");
			setDescription("");
			onOpenChange(false);
		}
	};

	return (
		<Dialog open={open} onOpenChange={handleClose}>
			<DialogContent className="sm:max-w-[425px]">
				<form onSubmit={handleSubmit}>
					<DialogHeader>
						<DialogTitle>Create Workspace</DialogTitle>
						<DialogDescription>
							Create a new workspace to organize your resources and
							deployments.
						</DialogDescription>
					</DialogHeader>

					<div className="grid gap-4 py-4">
						<div className="grid gap-2">
							<Label htmlFor="workspace-name">Workspace Name</Label>
							<Input
								id="workspace-name"
								placeholder="Production"
								value={workspaceName}
								onChange={(e) => setWorkspaceName(e.target.value)}
								disabled={isPending}
								autoFocus
							/>
						</div>

						<div className="grid gap-2">
							<Label htmlFor="workspace-description">
								Description{" "}
								<span className="text-muted-foreground">(optional)</span>
							</Label>
							<Textarea
								id="workspace-description"
								placeholder="Production environment for customer-facing applications"
								value={description}
								onChange={(e) => setDescription(e.target.value)}
								disabled={isPending}
								rows={3}
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
							type="submit"
							disabled={isPending || !workspaceName.trim()}
						>
							{isPending ? (
								<>
									<Loader className="w-4 h-4 mr-2" />
									Creating...
								</>
							) : (
								"Create Workspace"
							)}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}
