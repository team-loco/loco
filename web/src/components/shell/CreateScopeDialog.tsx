import { useMutation } from "@connectrpc/connect-query";
import { createWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useNavigate } from "react-router";

import { Field } from "@/components/design/Field";
import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { Textarea } from "@/components/design/Textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { toastConnectError } from "@/lib/error-handler";
import { workspacePath } from "@/lib/routes";

import { CreateOrgDialog } from "./CreateOrgDialog";

export type ScopeKind = "org" | "workspace";

export function CreateScopeDialog({
	kind,
	fixedOrgId,
	onOpenChange,
}: {
	kind: ScopeKind | null;
	/** Opened from a page of this organization: the workspace goes there, with no organization choice and no close button. */
	fixedOrgId?: string;
	onOpenChange: (open: boolean) => void;
}) {
	const navigate = useNavigate();
	if (kind === "org") {
		return (
			<CreateOrgDialog
				open
				onOpenChange={onOpenChange}
				onSuccess={(orgId, workspaceId) => {
					void navigate(workspacePath(orgId, workspaceId));
				}}
			/>
		);
	}
	return (
		<Dialog open={kind !== null} onOpenChange={onOpenChange}>
			{kind !== null && (
				<CreateWorkspaceForm
					fixedOrgId={fixedOrgId}
					onDone={() => {
						onOpenChange(false);
					}}
				/>
			)}
		</Dialog>
	);
}

function CreateWorkspaceForm({ fixedOrgId, onDone }: { fixedOrgId: string | undefined; onDone: () => void }) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const { orgs, activeOrgId } = useOrgWorkspace();
	const [name, setName] = useState("");
	const nameRef = useRef<HTMLInputElement>(null);
	const [description, setDescription] = useState("");
	const [pickedOrgId, setPickedOrgId] = useState(activeOrgId ?? orgs[0]?.id ?? "");
	const orgId = fixedOrgId ?? pickedOrgId;
	const choosable = fixedOrgId === undefined && orgs.length > 1;
	const wsMutation = useMutation(createWorkspace);
	const pending = wsMutation.isPending;

	const orgName = orgs.find((o) => o.id === orgId)?.name ?? "";
	const trimmed = name.trim();
	const maxLen = 63;
	const error = trimmed.length > maxLen ? "Too long" : null;
	const ok = trimmed !== "" && error === null && orgId !== "";

	const submit = async () => {
		if (!ok || pending) return;
		try {
			const res = await wsMutation.mutateAsync({
				orgId,
				name: trimmed,
				description: description.trim() === "" ? undefined : description.trim(),
			});
			await queryClient.invalidateQueries();
			onDone();
			void navigate(workspacePath(orgId, res.workspaceId));
		} catch (err) {
			toastConnectError(err, "Failed to create workspace");
		}
	};

	return (
		<DialogContent
			title={`New workspace in ${orgName}`}
			showCloseButton={fixedOrgId === undefined}
			initialFocus={nameRef}
		>
			<form
				onSubmit={(e) => {
					e.preventDefault();
					void submit();
				}}
				className="contents"
			>
				<DialogBody>
					<p className="m-0 text-[13px] leading-normal text-fg2">
						A workspace is a project inside {orgName}. Its resources, environments, members, tokens and webhooks live
						here.
					</p>
					{choosable && (
						<div className="flex flex-col gap-1.5">
							<span className="font-semibold">Organization</span>
							<ToggleGroup
								variant="segmented"
								value={[orgId]}
								onValueChange={(v: string[]) => {
									const next = v[0];
									if (next !== undefined) setPickedOrgId(next);
								}}
							>
								{orgs.map((o) => (
									<ToggleGroupItem key={o.id} value={o.id} className="h-[26px]! text-[12.5px]">
										{o.name}
									</ToggleGroupItem>
								))}
							</ToggleGroup>
						</div>
					)}
					<Field label="Name" error={error}>
						<Input
							ref={nameRef}
							autoComplete="off"
							value={name}
							placeholder="payments"
							aria-invalid={error !== null}
							className="h-9 text-[13.5px]"
							onChange={(e) => {
								setName(e.target.value);
							}}
						/>
					</Field>
					<Field label="Description">
						<Textarea
							rows={2}
							maxLength={256}
							value={description}
							className="text-[13.5px]"
							onChange={(e) => {
								setDescription(e.target.value);
							}}
						/>
					</Field>
				</DialogBody>
				<DialogFooter>
					<Button type="button" variant="outline" size="lg" onClick={onDone}>
						Cancel
					</Button>
					<Button type="submit" size="lg" disabled={!ok || pending}>
						Create workspace
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
