import { useMutation } from "@connectrpc/connect-query";
import type { Webhook } from "@gen/loco/webhook/v1/webhook_pb";
import { createWebhook, updateWebhook } from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { toast } from "@/lib/toast";
import { ChevronDownIcon, ChevronUpIcon } from "lucide-react";
import { useId, useRef, useState, type RefObject } from "react";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { InlineCode } from "@/components/design/InlineCode";
import { Input } from "@/components/design/Input";
import { Notice } from "@/components/design/Notice";
import { SecretValue } from "@/components/design/SecretValue";
import { Checkbox } from "@/components/design/Checkbox";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage } from "@/lib/error-handler";
import { pageSelection, setPage, toggleId } from "@/lib/selection";
import { cn } from "@/lib/utils";

import {
	chosenLabel,
	groupCountLabel,
	isHttpsUrl,
	isPrivateAddress,
	isUrlRejection,
	webhookEventGroups,
} from "./webhooks";

type Mode = "all" | "choose";

const GROUPS = webhookEventGroups();
const EVENT_COUNT = GROUPS.reduce((n, g) => n + g.types.length, 0);
const NO_TYPES: string[] = [];
const NAME_MAX = 100;

export function NewWebhookDialog({
	workspaceId,
	workspaceName,
	webhook,
	open,
	onClose,
	onCreated,
	initialTypes = NO_TYPES,
}: {
	initialTypes?: string[];
	workspaceId: string;
	workspaceName: string;
	webhook?: Webhook | undefined;
	open: boolean;
	onClose: () => void;
	onCreated: () => void;
}) {
	const [secret, setSecret] = useState<string | null>(null);
	const [pending, setPending] = useState(false);
	const nameRef = useRef<HTMLInputElement>(null);
	const close = () => {
		setSecret(null);
		onClose();
	};
	return (
		<Dialog
			open={open}
			disablePointerDismissal={secret !== null || pending}
			onOpenChange={(next) => {
				if (!next && !pending) close();
			}}
		>
			{open && (
				<DialogContent
					title={
						secret !== null ? (
							"Signing secret"
						) : webhook === undefined ? (
							<>
								New webhook in <InlineCode>{workspaceName}</InlineCode>
							</>
						) : (
							"Edit webhook"
						)
					}
					className={secret === null ? "w-[560px]" : undefined}
					showCloseButton={secret === null}
					initialFocus={nameRef}
				>
					{secret === null ? (
						<NewWebhookForm
							initialTypes={initialTypes}
							workspaceId={workspaceId}
							webhook={webhook}
							nameRef={nameRef}
							onCancel={close}
							onPending={setPending}
							onCreated={(s) => {
								if (s === null) close();
								else setSecret(s);
								onCreated();
							}}
						/>
					) : (
						<SigningSecret secret={secret} onDone={close} />
					)}
				</DialogContent>
			)}
		</Dialog>
	);
}

function NewWebhookForm({
	workspaceId,
	initialTypes,
	webhook,
	nameRef,
	onCancel,
	onPending,
	onCreated,
}: {
	initialTypes: string[];
	workspaceId: string;
	nameRef: RefObject<HTMLInputElement | null>;
	onCancel: () => void;
	onPending: (pending: boolean) => void;
	webhook?: Webhook | undefined;
	onCreated: (secret: string | null) => void;
}) {
	const create = useMutation(createWebhook);
	const update = useMutation(updateWebhook);
	const mutation = webhook === undefined ? create : update;
	const [name, setName] = useState(webhook?.name ?? "");
	const [url, setUrl] = useState(webhook?.url ?? "");
	const [mode, setMode] = useState<Mode>((webhook?.eventTypes ?? initialTypes).length > 0 ? "choose" : "all");
	const [types, setTypes] = useState<ReadonlySet<string>>(() => new Set(webhook?.eventTypes ?? initialTypes));

	const target = url.trim();
	const rejected = isUrlRejection(mutation.error);
	const httpsUrl = isHttpsUrl(target);
	const urlError = isPrivateAddress(target)
		? "Private and reserved addresses are blocked."
		: target !== "" && !httpsUrl
			? "Use an https:// URL."
			: rejected
				? getErrorMessage(mutation.error)
				: null;
	const ok = httpsUrl && !isPrivateAddress(target) && (mode === "all" || types.size > 0);

	const submit = () => {
		if (!ok || mutation.isPending) return;
		const input = { workspaceId, name: name.trim(), url: target, eventTypes: mode === "all" ? [] : [...types] };
		onPending(true);
		if (webhook !== undefined) {
			update.mutate(
				{ ...input, webhookId: webhook.id },
				{
					onSuccess: () => {
						toast.success("Saved webhook");
						onCreated(null);
					},
					onSettled: () => {
						onPending(false);
					},
				},
			);
		} else {
			create.mutate(input, {
				onSuccess: (res) => {
					onCreated(res.secret);
				},
				onSettled: () => {
					onPending(false);
				},
			});
		}
	};

	return (
		<form
			noValidate
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				submit();
			}}
		>
			<DialogBody>
				<Field
					label={
						<>
							Name <span className="font-normal text-fg3">(optional)</span>
						</>
					}
					hint="Shown in the webhook list."
				>
					<Input
						ref={nameRef}
						autoComplete="off"
						maxLength={NAME_MAX}
						value={name}
						placeholder="Billing alerts"
						className="h-9"
						onChange={(e) => {
							setName(e.target.value);
						}}
					/>
				</Field>
				<Field label="Endpoint URL" error={urlError}>
					<Input
						autoComplete="off"
						spellCheck={false}
						value={url}
						placeholder="https://example.com/hooks"
						aria-invalid={urlError !== null}
						className="h-9 font-mono text-[13.5px]"
						onChange={(e) => {
							setUrl(e.target.value);
							mutation.reset();
						}}
					/>
				</Field>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">Events</span>
					<ToggleGroup
						variant="segmented"
						aria-label="Events"
						className="w-max"
						value={[mode]}
						onValueChange={(v: string[]) => {
							if (v[0] === "all" || v[0] === "choose") setMode(v[0]);
						}}
					>
						<ToggleGroupItem value="all">All events</ToggleGroupItem>
						<ToggleGroupItem value="choose">Choose events</ToggleGroupItem>
					</ToggleGroup>
				</div>
				{mode === "choose" && (
					<EventPicker
						selected={types}
						onChange={(next) => {
							setTypes(next);
							mutation.reset();
						}}
					/>
				)}
				{mutation.error !== null && !rejected && (
					<span className="text-[12.5px] text-bad-fg">
						{getErrorMessage(
							mutation.error,
							webhook === undefined ? "Could not add the webhook" : "Could not save the webhook",
						)}
					</span>
				)}
			</DialogBody>
			<DialogFooter>
				<Button type="button" variant="outline" disabled={mutation.isPending} onClick={onCancel}>
					Cancel
				</Button>
				<Button type="submit" disabled={!ok}>
					{webhook === undefined
						? mutation.isPending
							? "Creating…"
							: "Create webhook"
						: mutation.isPending
							? "Saving…"
							: "Save"}
				</Button>
			</DialogFooter>
		</form>
	);
}

function EventPicker({
	selected,
	onChange,
}: {
	selected: ReadonlySet<string>;
	onChange: (next: ReadonlySet<string>) => void;
}) {
	const [opened, setOpened] = useState<string | null>(() => {
		const [first] = selected;
		return GROUPS.find((g) => first !== undefined && g.types.includes(first))?.label ?? null;
	});
	const id = useId();
	return (
		<>
			<div
				role="group"
				aria-label="Events to send"
				className="max-h-[300px] overflow-y-auto rounded-card p-1 ring-1 ring-line ring-inset"
			>
				{GROUPS.map((g, i) => {
					const state = pageSelection(selected, g.types);
					const open = opened === g.label;
					const groupId = `${id}-${i.toString()}`;
					const count = groupCountLabel(state.count, g.types.length);
					return (
						<div key={g.label} className="border-line not-first:border-t">
							<div className="flex items-center gap-1 rounded-md hover:bg-panel focus-within:bg-panel">
								<label
									htmlFor={groupId}
									className="flex min-h-9 flex-1 items-center gap-2.5 rounded-md px-2 text-[13.5px]"
								>
									<Checkbox
										id={groupId}
										aria-label={`${g.label} ${count}`}
										checked={state.all}
										indeterminate={state.some && !state.all}
										onCheckedChange={(on) => {
											onChange(setPage(selected, g.types, on));
										}}
									/>
									<span className="font-medium">{g.label}</span>
									<span className={cn("ml-auto text-[12px]", state.some ? "text-fg2" : "text-fg3")}>{count}</span>
								</label>
								<Button
									type="button"
									variant="ghost"
									size="icon-xs"
									aria-expanded={open}
									aria-label={`${open ? "Hide" : "Show"} ${g.label.toLowerCase()} events`}
									onClick={() => {
										setOpened(open ? null : g.label);
									}}
								>
									{open ? <ChevronUpIcon /> : <ChevronDownIcon />}
								</Button>
							</div>
							{open && (
								<div className="flex flex-col pb-1.5 pl-[26px]">
									{g.types.map((t) => (
										<label
											key={t}
											htmlFor={`${groupId}-${t}`}
											className="flex min-h-7.5 items-center gap-2.5 rounded-md px-2 font-mono text-[12.5px] text-fg2 hover:bg-panel has-data-checked:text-foreground"
										>
											<Checkbox
												id={`${groupId}-${t}`}
												checked={selected.has(t)}
												onCheckedChange={() => {
													onChange(toggleId(selected, t));
												}}
											/>
											{t}
										</label>
									))}
								</div>
							)}
						</div>
					);
				})}
			</div>
			<span className="-mt-1 text-[12px] text-fg3">{chosenLabel(selected.size, EVENT_COUNT)}</span>
		</>
	);
}

export function SigningSecret({ secret, onDone }: { secret: string; onDone: () => void }) {
	const [shown, setShown] = useState(false);
	return (
		<>
			<DialogBody>
				<SecretValue
					secret={secret}
					shown={shown}
					onShownChange={setShown}
					noun="signing secret"
					toastText="Copied signing secret"
				/>
				<Notice tone="warn">Copy it now. You won&apos;t be able to see it again.</Notice>
				<p className="m-0 text-[13.5px] text-fg2">
					Use it to verify the <InlineCode>webhook-signature</InlineCode> header.
				</p>
			</DialogBody>
			<DialogFooter>
				<Button onClick={onDone}>Done</Button>
			</DialogFooter>
		</>
	);
}
