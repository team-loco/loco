import { getOption } from "@bufbuild/protobuf";
import { useMutation } from "@connectrpc/connect-query";
import { useRef, useState } from "react";
import { toast } from "sonner";

import { field } from "@gen/buf/validate/validate_pb";
import { createOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { CreateOrgRequestSchema } from "@gen/loco/org/v1/org_pb";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { toastConnectError } from "@/lib/error-handler";

const nameRules = getOption(CreateOrgRequestSchema.field.name, field).type;
if (nameRules.case !== "string") throw new Error("Organization name must have string validation rules");
const maxNameLength = Number(nameRules.value.maxLen);

export function CreateOrgDialog({
	open,
	onOpenChange,
	onSuccess,
	existingNames,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onSuccess: (orgId: string) => void;
	existingNames: string[];
}) {
	const [orgName, setOrgName] = useState("");
	const nameRef = useRef<HTMLInputElement>(null);
	const { mutate: mutateCreateOrg, isPending } = useMutation(createOrg);
	const trimmed = orgName.trim();

	const nameError =
		trimmed === ""
			? null
			: Array.from(trimmed).length > maxNameLength
				? `Use at most ${maxNameLength.toString()} characters.`
				: existingNames.includes(trimmed)
					? `${trimmed} is taken.`
					: null;

	const handleSubmit = (e: React.SubmitEvent<HTMLFormElement>) => {
		e.preventDefault();
		if (trimmed === "" || nameError !== null || isPending) {
			return;
		}
		mutateCreateOrg(
			{ name: trimmed },
			{
				onSuccess: (response) => {
					if (response.orgId) {
						toast.success(`Created ${trimmed}`, {
							description: "With a default workspace",
							action: {
								label: "Open dashboard",
								onClick: () => {
									onSuccess(response.orgId);
								},
							},
						});
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
			<DialogContent title="New organization" initialFocus={nameRef}>
				<form onSubmit={handleSubmit} className="flex min-h-0 flex-col">
					<DialogBody>
						<Field label="Name" error={nameError}>
							<Input
								ref={nameRef}
								value={orgName}
								onChange={(e) => {
									setOrgName(e.target.value);
								}}
								placeholder="acme"
								className="font-mono"
								disabled={isPending}
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
						<Button type="submit" size="lg" disabled={isPending || trimmed === "" || nameError !== null}>
							{isPending ? "Creating..." : "Create"}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}
