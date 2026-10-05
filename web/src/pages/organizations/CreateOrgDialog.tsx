import { useMutation } from "@connectrpc/connect-query";
import { Building2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { createOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { toastConnectError } from "@/lib/error-handler";

export function CreateOrgDialog({
	open,
	onOpenChange,
	onSuccess,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onSuccess: (orgId: string) => void;
}) {
	const [orgName, setOrgName] = useState("");
	const { mutate: mutateCreateOrg, isPending } = useMutation(createOrg);
	const trimmed = orgName.trim();

	const handleSubmit = (e: React.FormEvent) => {
		e.preventDefault();
		if (trimmed === "") {
			return;
		}
		mutateCreateOrg(
			{ name: trimmed },
			{
				onSuccess: (response) => {
					if (response.orgId) {
						toast.success(`Organization "${trimmed}" created`);
						setOrgName("");
						onOpenChange(false);
						onSuccess(response.orgId);
					}
				},
				onError: (error) => {
					toastConnectError(error, "Failed to create organization");
				},
			},
		);
	};

	const handleOpenChange = (next: boolean) => {
		if (isPending) {
			return;
		}
		if (!next) {
			setOrgName("");
		}
		onOpenChange(next);
	};

	return (
		<Dialog open={open} onOpenChange={handleOpenChange}>
			<DialogContent title="New organization" icon={<Building2 />}>
				<form onSubmit={handleSubmit} className="flex min-h-0 flex-col">
					<DialogBody>
						<DialogDescription>
							Organizations are used to manage workspaces, resources, and billing.
						</DialogDescription>
						<Field label="Name" strong>
							<Input
								value={orgName}
								onChange={(e) => {
									setOrgName(e.target.value);
								}}
								placeholder="team-loco"
								disabled={isPending}
								autoFocus
							/>
						</Field>
					</DialogBody>
					<DialogFooter>
						<Button
							type="button"
							variant="outline"
							size="lg"
							onClick={() => {
								handleOpenChange(false);
							}}
							disabled={isPending}
						>
							Cancel
						</Button>
						<Button type="submit" size="lg" disabled={isPending || trimmed === ""}>
							{isPending ? "Creating..." : "Create"}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}
