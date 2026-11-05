import { useMutation } from "@connectrpc/connect-query";
import { createWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { useQueryClient } from "@tanstack/react-query";
import { LayersIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Field } from "@/components/design/Field";
import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { Textarea } from "@/components/design/Textarea";
import { toastConnectError } from "@/lib/error-handler";

export function NewWorkspaceDialog({
	open,
	onOpenChange,
	orgId,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	orgId: string;
}) {
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			{open && (
				<NewWorkspaceForm
					orgId={orgId}
					onDone={() => {
						onOpenChange(false);
					}}
				/>
			)}
		</Dialog>
	);
}

function NewWorkspaceForm({ orgId, onDone }: { orgId: string; onDone: () => void }) {
	const queryClient = useQueryClient();
	const create = useMutation(createWorkspace);
	const [name, setName] = useState("");
	const [desc, setDesc] = useState("");

	const nameT = name.trim();
	const error = nameT.length > 63 ? "Max 63 characters" : null;
	const ok = nameT !== "" && error === null;

	const submit = () => {
		if (!ok || create.isPending) return;
		const description = desc.trim();
		create.mutate(
			{ orgId, name: nameT, description: description === "" ? undefined : description },
			{
				onSuccess: () => {
					toast.success(`Created ${nameT}`);
					void queryClient.invalidateQueries();
					onDone();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to create workspace");
				},
			},
		);
	};

	return (
		<DialogContent title="New workspace" icon={<LayersIcon />} className="w-[480px]">
			<form
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					submit();
				}}
			>
				<DialogBody>
					<Field label="Name" strong error={error}>
						<Input
							autoFocus
							autoComplete="off"
							value={name}
							placeholder="payments"
							aria-invalid={error !== null}
							className="h-9 text-[13.5px]"
							onChange={(e) => {
								setName(e.target.value.toLowerCase());
							}}
						/>
					</Field>
					<Field label="Description" strong>
						<Textarea
							rows={2}
							maxLength={256}
							value={desc}
							className="text-[13.5px]"
							onChange={(e) => {
								setDesc(e.target.value);
							}}
						/>
					</Field>
				</DialogBody>
				<DialogFooter>
					<Button type="button" variant="outline" size="lg" className="px-3" onClick={onDone}>
						Cancel
					</Button>
					<Button type="submit" size="lg" disabled={!ok || create.isPending}>
						Create workspace
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
