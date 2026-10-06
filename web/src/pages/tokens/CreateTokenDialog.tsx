import { useMutation } from "@connectrpc/connect-query";
import { KeyRoundIcon } from "lucide-react";
import { useState } from "react";
import { createToken } from "@gen/loco/token/v1/token-TokenService_connectquery";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { toastConnectError } from "@/lib/error-handler";
import { pluralize } from "@/lib/format";
import { DAY_MS } from "@/lib/time";
import { cn } from "@/lib/utils";

import { AccessPicker } from "./AccessPicker";
import type { AccessItem } from "./AccessPicker";
import { EXPIRY_DAYS, grantsToScopes } from "./model";
import type { EntityNode, EntityTree, ExpiryDays, HeldScopes, Level } from "./model";
import type { Owner } from "./useTokenData";

export interface CreateDraft {
	name: string;
	items: AccessItem[];
}

export function CreateTokenDialog({
	draft,
	owner,
	tree,
	held,
	takenNames,
	resourcesLoading,
	onClose,
	onCreated,
}: {
	draft: CreateDraft | null;
	owner: Owner;
	tree: EntityTree;
	held: HeldScopes;
	takenNames: string[];
	resourcesLoading: boolean;
	onClose: () => void;
	onCreated: (name: string, secret: string) => void;
}) {
	return (
		<Dialog
			open={draft !== null}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			{draft !== null && (
				<CreateTokenForm
					draft={draft}
					owner={owner}
					tree={tree}
					held={held}
					takenNames={takenNames}
					resourcesLoading={resourcesLoading}
					onClose={onClose}
					onCreated={onCreated}
				/>
			)}
		</Dialog>
	);
}

function CreateTokenForm({
	draft,
	owner,
	tree,
	held,
	takenNames,
	resourcesLoading,
	onClose,
	onCreated,
}: {
	draft: CreateDraft;
	owner: Owner;
	tree: EntityTree;
	held: HeldScopes;
	takenNames: string[];
	resourcesLoading: boolean;
	onClose: () => void;
	onCreated: (name: string, secret: string) => void;
}) {
	const [name, setName] = useState(draft.name);
	const [days, setDays] = useState<ExpiryDays>(30);
	const [items, setItems] = useState<AccessItem[]>(draft.items);
	const [tried, setTried] = useState(false);
	const mutation = useMutation(createToken);

	const trimmed = name.trim();
	const taken = takenNames.includes(trimmed);
	const nameErr = tried || trimmed !== "" ? (trimmed === "" ? "Name required" : taken ? `${trimmed} already exists` : null) : null;
	const accessErr = tried && items.length === 0 ? "Add at least one workspace or resource" : null;
	const ready = trimmed !== "" && !taken && items.length > 0 && owner.id !== null;

	const submit = async () => {
		if (mutation.isPending) return;
		if (!ready || owner.id === null) {
			setTried(true);
			return;
		}
		const resolved: { node: EntityNode; level: Level }[] = [];
		for (const it of items) {
			const node = tree.nodes.get(it.key);
			if (node !== undefined) resolved.push({ node, level: it.level });
		}
		try {
			const res = await mutation.mutateAsync({
				name: trimmed,
				entityType: owner.entityType,
				entityId: owner.id,
				scopes: grantsToScopes(resolved),
				expiresInSec: BigInt((days * DAY_MS) / 1000),
			});
			onCreated(trimmed, res.token);
		} catch (err) {
			toastConnectError(err, "Failed to create token");
		}
	};

	return (
		<DialogContent title={`New ${owner.noun} token`} icon={<KeyRoundIcon />} className="top-[7vh] max-h-[86vh] w-[600px]">
			<form
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					void submit();
				}}
			>
				<DialogBody className="gap-[18px] p-[18px]">
					<div className="grid grid-cols-1 items-start gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
						<Field label="Name" strong error={nameErr}>
							<Input
								autoFocus
								autoComplete="off"
								value={name}
								maxLength={100}
								placeholder="github-actions-deploy"
								aria-invalid={nameErr !== null}
								className="h-9 text-[13.5px]"
								onChange={(e) => {
									setName(e.target.value);
								}}
							/>
						</Field>
						<div className="flex flex-col gap-1.5">
							<span className="font-semibold">Expires</span>
							<ToggleGroup
								variant="segmented"
								value={[days.toString()]}
								onValueChange={(v: string[]) => {
									const next = EXPIRY_DAYS.find((d) => d.toString() === v[0]);
									if (next !== undefined) setDays(next);
								}}
							>
								{EXPIRY_DAYS.map((d) => (
									<ToggleGroupItem key={d} value={d.toString()} className="h-[26px]! text-[12.5px]">
										{pluralize(d, "day")}
									</ToggleGroupItem>
								))}
							</ToggleGroup>
						</div>
					</div>
					<div className="flex flex-col gap-2">
						<span className="font-semibold">Access</span>
						<AccessPicker
							tree={tree}
							held={held}
							items={items}
							allowUser={owner.key === "personal"}
							resourcesLoading={resourcesLoading}
							invalid={accessErr !== null}
							onChange={setItems}
						/>
						{accessErr !== null && <span className="text-sm text-bad-fg">{accessErr}</span>}
						<span className="text-sm text-fg3">You can only grant access you hold yourself.</span>
					</div>
				</DialogBody>
				<DialogFooter className="px-[18px]">
					<Button type="button" variant="outline" size="lg" className="px-3" onClick={onClose}>
						Cancel
					</Button>
					<Button type="submit" size="lg" disabled={mutation.isPending} className={cn(!ready && "opacity-55")}>
						{mutation.isPending ? "Creating…" : "Create token"}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
