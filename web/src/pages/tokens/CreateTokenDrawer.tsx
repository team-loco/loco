import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation } from "@connectrpc/connect-query";
import { CheckIcon } from "lucide-react";
import { useRef, useState, type RefObject } from "react";
import { createToken } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { TokenService } from "@gen/loco/token/v1/token_pb";

import { BASE_URL } from "@/auth/connect-transport";
import { Button } from "@/components/design/Button";
import { CodeBlock } from "@/components/design/CodeBlock";
import { CopyButton } from "@/components/design/CopyButton";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { pluralize } from "@/lib/format";
import { DAY_MS, formatMonthDayYear } from "@/lib/time";

import { AccessPicker } from "./AccessPicker";
import type { AccessItem } from "./AccessPicker";
import { ownerIcon } from "./icons";
import { allowsFor, EXPIRY_DAYS, grantsToScopes, OWNER_KEYS, OWNER_TITLE } from "./model";
import type { EntityNode, ExpiryDays, Level, OwnerKey } from "./model";
import type { TokenData } from "./useTokenData";

export interface CreateDraft {
	owner: OwnerKey;
	name: string;
	items: AccessItem[];
}

const NAME_MAX = 100;
const DEFAULT_EXPIRY: ExpiryDays = 30;
const MS_PER_SECOND = 1000;

function apiExample(): string {
	return [
		'curl -H "Authorization: Bearer $LOCO_TOKEN" \\',
		"  -H 'Content-Type: application/json' -d '{}' \\",
		`  ${BASE_URL}/${TokenService.typeName}/${TokenService.method.getScopes.name}`,
	].join("\n");
}

export function CreateTokenDrawer({
	draft,
	data,
	now,
	onClose,
	onCreated,
}: {
	draft: CreateDraft | null;
	data: TokenData;
	now: number;
	onClose: () => void;
	onCreated: (owner: OwnerKey) => void;
}) {
	const [secret, setSecret] = useState<{ name: string; value: string } | null>(null);
	const nameRef = useRef<HTMLInputElement>(null);
	return (
		<Sheet
			open={draft !== null}
			disablePointerDismissal={secret !== null}
			onOpenChange={(open) => {
				if (open) return;
				setSecret(null);
				onClose();
			}}
		>
			<SheetContent size="wide" initialFocus={nameRef}>
				<SheetHeader>
					<SheetTitle>{secret === null ? "New token" : "Token created"}</SheetTitle>
				</SheetHeader>
				{secret !== null && (
					<Reveal
						name={secret.name}
						secret={secret.value}
						onDone={() => {
							setSecret(null);
							onClose();
						}}
					/>
				)}
				{secret === null && draft !== null && (
					<CreateForm
						nameRef={nameRef}
						draft={draft}
						data={data}
						now={now}
						onCancel={onClose}
						onCreated={(owner, name, value) => {
							setSecret({ name, value });
							onCreated(owner);
						}}
					/>
				)}
			</SheetContent>
		</Sheet>
	);
}

function CreateForm({
	nameRef,
	draft,
	data,
	now,
	onCancel,
	onCreated,
}: {
	nameRef: RefObject<HTMLInputElement | null>;
	draft: CreateDraft;
	data: TokenData;
	now: number;
	onCancel: () => void;
	onCreated: (owner: OwnerKey, name: string, secret: string) => void;
}) {
	const [ownerKey, setOwnerKey] = useState(draft.owner);
	const [name, setName] = useState(draft.name);
	const [days, setDays] = useState<ExpiryDays>(DEFAULT_EXPIRY);
	const [items, setItems] = useState<AccessItem[]>(draft.items);
	const [tried, setTried] = useState(false);
	const [nameTaken, setNameTaken] = useState<string | null>(null);
	const mutation = useMutation(createToken);
	const owner = data.owners[ownerKey].owner;

	const trimmed = name.trim();
	const nameErr = nameTaken ?? (tried && trimmed === "" ? "Enter a name" : null);
	const accessErr = tried && items.length === 0 ? "Add at least one workspace or resource" : null;
	const resolved: { node: EntityNode; level: Level }[] = items.flatMap((it) => {
		const node = data.tree.nodes.get(it.key);
		return node === undefined ? [] : [{ node, level: it.level }];
	});
	const allows = allowsFor(resolved.map((r) => ({ kind: r.node.kind, level: r.level })));
	const createHint = owner.canCreate ? undefined : `You need write access to ${owner.label} to create tokens`;

	const submit = async () => {
		if (mutation.isPending || owner.id === null) return;
		if (trimmed === "" || items.length === 0) {
			setTried(true);
			return;
		}
		try {
			const res = await mutation.mutateAsync({
				name: trimmed,
				entityType: owner.entityType,
				entityId: owner.id,
				scopes: grantsToScopes(resolved),
				expiresInSec: BigInt((days * DAY_MS) / MS_PER_SECOND),
			});
			onCreated(ownerKey, trimmed, res.token);
		} catch (err) {
			if (err instanceof ConnectError && err.code === Code.AlreadyExists) {
				setNameTaken(getErrorMessage(err));
				return;
			}
			toastConnectError(err, "Failed to create token");
		}
	};

	return (
		<form
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				void submit();
			}}
		>
			<SheetBody>
				<Field label="Name" error={nameErr}>
					<Input
						ref={nameRef}
						autoComplete="off"
						value={name}
						maxLength={NAME_MAX}
						placeholder="ci-deploy"
						aria-invalid={nameErr !== null}
						className="font-mono"
						onChange={(e) => {
							setName(e.target.value);
							setNameTaken(null);
						}}
					/>
				</Field>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">Owner</span>
					<ToggleGroup
						variant="segmented"
						aria-label="Owner"
						className="w-max"
						value={[ownerKey]}
						onValueChange={(v: string[]) => {
							const next = OWNER_KEYS.find((k) => k === v[0]);
							if (next === undefined || next === ownerKey) return;
							setOwnerKey(next);
							setItems([]);
							setNameTaken(null);
						}}
					>
						{OWNER_KEYS.map((k) => {
							const o = data.owners[k].owner;
							return (
								<ToggleGroupItem
									key={k}
									value={k}
									disabled={!o.canCreate}
									title={o.canCreate ? o.label : `You need write access to ${o.label}`}
									className="disabled:opacity-45 [&_svg]:size-[13px] [&_svg]:text-fg3"
								>
									{ownerIcon(k)}
									{OWNER_TITLE[k]}
								</ToggleGroupItem>
							);
						})}
					</ToggleGroup>
				</div>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">Expires</span>
					<ToggleGroup
						variant="segmented"
						aria-label="Expiry"
						className="w-max"
						value={[days.toString()]}
						onValueChange={(v: string[]) => {
							const next = EXPIRY_DAYS.find((d) => d.toString() === v[0]);
							if (next !== undefined) setDays(next);
						}}
					>
						{EXPIRY_DAYS.map((d) => (
							<ToggleGroupItem key={d} value={d.toString()}>
								{pluralize(d, "day")}
							</ToggleGroupItem>
						))}
					</ToggleGroup>
					<span className="text-[12.5px] text-fg3">Expires {formatMonthDayYear(now + days * DAY_MS)}</span>
				</div>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">Permissions</span>
					<AccessPicker
						key={ownerKey}
						tree={data.tree}
						held={data.held}
						items={items}
						allowUser={ownerKey === "personal"}
						resourcesLoading={data.resourcesLoading}
						resourcesError={data.resourcesError}
						retryResources={data.retryResources}
						invalid={accessErr !== null}
						onChange={setItems}
					/>
					{accessErr !== null && <span className="text-[12.5px] text-bad-fg">{accessErr}</span>}
					<span className="text-[12.5px] text-fg3">You can only grant access you hold yourself.</span>
					{allows.length > 0 && (
						<ul className="m-0 mt-1.5 flex list-none flex-col gap-1.5 p-0">
							{allows.map((line) => (
								<li key={line} className="flex items-start gap-2 text-[12.5px] text-fg2">
									<CheckIcon className="mt-0.5 size-3.5 shrink-0 text-ok-fg" />
									{line}
								</li>
							))}
						</ul>
					)}
				</div>
			</SheetBody>
			<SheetFooter>
				<Button type="button" variant="outline" onClick={onCancel}>
					Cancel
				</Button>
				<Button type="submit" disabled={mutation.isPending || !owner.canCreate} title={createHint}>
					{mutation.isPending ? "Creating…" : "Create token"}
				</Button>
			</SheetFooter>
		</form>
	);
}

function Reveal({ name, secret, onDone }: { name: string; secret: string; onDone: () => void }) {
	return (
		<>
			<SheetBody>
				<span className="flex items-center gap-2 text-[13.5px]">
					<StatusGlyph glyph="ok" />
					<span>
						<span className="font-mono">{name}</span> is ready.
					</span>
				</span>
				<div className="flex items-center gap-2.5 rounded-lg bg-panel py-2.5 pr-2.5 pl-3 font-mono text-[12.5px] break-all">
					<span className="flex-1 select-all">{secret}</span>
					<CopyButton text={secret} />
				</div>
				<span className="text-[13px] text-fg2">Copy it now. You won&apos;t be able to see it again.</span>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">Call the API with it</span>
					<CodeBlock>{apiExample()}</CodeBlock>
					<span className="text-[12.5px] text-fg3">
						With the token in <span className="font-mono">LOCO_TOKEN</span>, this lists the scopes it holds.
					</span>
				</div>
				<p className="m-0 text-[13px] text-fg2">
					To deploy from CI with a workspace write grant, save the token as <code>LOCO_TOKEN</code> and set{" "}
					<code>LOCO_HOST</code> to the API URL. Run <code>loco deploy --yes</code> with <code>--org</code>,{" "}
					<code>--workspace</code> (the workspace ID), and <code>--env</code>.
				</p>
			</SheetBody>
			<SheetFooter>
				<Button onClick={onDone}>Done</Button>
			</SheetFooter>
		</>
	);
}
