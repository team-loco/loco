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
import { createOrg } from "@/gen/loco/org/v1";
import { useMutation } from "@connectrpc/connect-query";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/error-handler";
import Loader from "@/assets/loader.svg?react";

interface CreateOrgDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onSuccess?: (orgId: bigint) => void;
}

export function CreateOrgDialog({
	open,
	onOpenChange,
	onSuccess,
}: CreateOrgDialogProps) {
	const navigate = useNavigate();
	const [orgName, setOrgName] = useState("");

	const { mutate: mutateCreateOrg, isPending } = useMutation(createOrg);

	const handleSubmit = (e: React.FormEvent) => {
		e.preventDefault();

		if (!orgName.trim()) {
			toast.error("Organization name is required");
			return;
		}

		mutateCreateOrg(
			{ name: orgName.trim() },
			{
				onSuccess: (response) => {
					const newOrgId = response.orgId;
					if (newOrgId) {
						toast.success(`Organization "${orgName}" created`);
						setOrgName("");
						onOpenChange(false);

						// Call success callback if provided, otherwise navigate
						if (onSuccess) {
							onSuccess(newOrgId);
						} else {
							// Switch to new org and navigate to dashboard
							navigate(`/dashboard?org=${newOrgId}`);
						}
					}
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to create organization"));
				},
			}
		);
	};

	const handleClose = () => {
		if (!isPending) {
			setOrgName("");
			onOpenChange(false);
		}
	};

	return (
		<Dialog open={open} onOpenChange={handleClose}>
			<DialogContent className="sm:max-w-[650px]">
				<form onSubmit={handleSubmit}>
					<DialogHeader>
						<DialogTitle>Create Organization</DialogTitle>
						<DialogDescription>
							Organizations are used to manage workspaces, resources, and
							billing.
						</DialogDescription>
					</DialogHeader>

					<div className="grid gap-4 py-4">
						<div className="grid gap-2">
							<Label htmlFor="org-name">Organization Name</Label>
							<Input
								id="org-name"
								value={orgName}
								onChange={(e) => setOrgName(e.target.value)}
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
						<Button type="submit" disabled={isPending || !orgName.trim()}>
							{isPending ? (
								<>
									<Loader className="w-4 h-4 mr-2" />
									Creating...
								</>
							) : (
								"Create Organization"
							)}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}
