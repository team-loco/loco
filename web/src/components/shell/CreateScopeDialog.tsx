import { useMutation } from "@connectrpc/connect-query";
import { createOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { createWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { useQueryClient } from "@tanstack/react-query";
import { Building2Icon, LayersIcon } from "lucide-react";
import { useState } from "react";
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

export type ScopeKind = "org" | "workspace";

const NAME_RE = /^[a-z0-9][a-z0-9-]*$/;

export function CreateScopeDialog({
	kind,
	onOpenChange,
}: {
	kind: ScopeKind | null;
	onOpenChange: (open: boolean) => void;
}) {
	return (
		<Dialog open={kind !== null} onOpenChange={onOpenChange}>
			{kind !== null && <CreateScopeForm key={kind} kind={kind} onDone={() => { onOpenChange(false); }} />}
		</Dialog>
	);
}

function CreateScopeForm({ kind, onDone }: { kind: ScopeKind; onDone: () => void }) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const { orgs, activeOrgId } = useOrgWorkspace();
	const [name, setName] = useState("");
	const [description, setDescription] = useState("");
	const [orgId, setOrgId] = useState(activeOrgId ?? orgs[0]?.id ?? "");
	const orgMutation = useMutation(createOrg);
	const wsMutation = useMutation(createWorkspace);
	const pending = orgMutation.isPending || wsMutation.isPending;

	const isOrg = kind === "org";
	const trimmed = name.trim();
	const maxLen = isOrg ? 100 : 63;
	const error =
		trimmed === ""
			? null
			: trimmed.length > maxLen
				? "Too long"
				: !NAME_RE.test(trimmed)
					? "Lowercase letters, numbers and hyphens"
					: null;
	const ok = trimmed !== "" && error === null && (isOrg || orgId !== "");

	const submit = async () => {
		if (!ok || pending) return;
		try {
			if (isOrg) {
				const res = await orgMutation.mutateAsync({ name: trimmed });
				const ws = await wsMutation.mutateAsync({ orgId: res.orgId, name: "default" });
				await queryClient.invalidateQueries();
				onDone();
				void navigate(workspacePath(res.orgId, ws.workspaceId));
			} else {
				const res = await wsMutation.mutateAsync({
					orgId,
					name: trimmed,
					description: description.trim() === "" ? undefined : description.trim(),
				});
				await queryClient.invalidateQueries();
				onDone();
				void navigate(workspacePath(orgId, res.workspaceId));
			}
		} catch (err) {
			toastConnectError(err, isOrg ? "Failed to create organization" : "Failed to create workspace");
		}
	};

	return (
		<DialogContent
			title={isOrg ? "New organization" : "New workspace"}
			icon={isOrg ? <Building2Icon /> : <LayersIcon />}
		>
			<form
				onSubmit={(e) => {
					e.preventDefault();
					void submit();
				}}
				className="contents"
			>
				<DialogBody>
					{!isOrg && orgs.length > 1 && (
						<div className="flex flex-col gap-1.5">
							<span className="font-semibold">Organization</span>
							<ToggleGroup
								variant="segmented"
								value={[orgId]}
								onValueChange={(v: string[]) => {
									const next = v[0];
									if (next !== undefined) setOrgId(next);
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
					<Field label="Name" strong error={error}>
						<Input
							autoFocus
							autoComplete="off"
							value={name}
							placeholder={isOrg ? "acme-labs" : "payments"}
							aria-invalid={error !== null}
							className="h-9 text-[13.5px]"
							onChange={(e) => { setName(e.target.value.toLowerCase()); }}
						/>
					</Field>
					{!isOrg && (
						<Field label="Description" strong>
							<Textarea
								rows={2}
								maxLength={256}
								value={description}
								className="text-[13.5px]"
								onChange={(e) => { setDescription(e.target.value); }}
							/>
						</Field>
					)}
				</DialogBody>
				<DialogFooter>
					<Button type="button" variant="outline" size="lg" onClick={onDone}>
						Cancel
					</Button>
					<Button type="submit" size="lg" disabled={!ok || pending}>
						{isOrg ? "Create organization" : "Create workspace"}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
