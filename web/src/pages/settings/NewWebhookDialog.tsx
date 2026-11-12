import { useMutation } from "@connectrpc/connect-query";
import type { Webhook } from "@gen/loco/webhook/v1/webhook_pb";
import { createWebhook, updateWebhook } from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useRef, useState, type RefObject } from "react";

import { Button } from "@/components/design/Button";
import { CopyButton } from "@/components/design/CopyButton";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { ToggleChip } from "@/components/design/ToggleChip";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage } from "@/lib/error-handler";
import { pageSelection, setPage, toggleId } from "@/lib/selection";

import { groupCountLabel, isHttpsUrl, isUrlRejection, webhookEventGroups } from "./webhooks";

type Mode = "all" | "choose";

const GROUPS = webhookEventGroups();

export function NewWebhookDialog({
	workspaceId,
	workspaceName,
	webhook,
	open,
	onClose,
	onCreated,
}: {
	workspaceId: string;
	workspaceName: string;
	webhook?: Webhook | undefined;
	open: boolean;
	onClose: () => void;
	onCreated: () => void;
}) {
	const [secret, setSecret] = useState<string | null>(null);
	const urlRef = useRef<HTMLInputElement>(null);
	const close = () => {
		setSecret(null);
		onClose();
	};
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next) close();
			}}
		>
			{open && (
				<DialogContent
					title={
						secret !== null
							? "Signing secret"
							: webhook === undefined
								? `New webhook in ${workspaceName}`
								: "Edit webhook"
					}
					className={secret === null ? "w-[560px]" : undefined}
					initialFocus={urlRef}
				>
					{secret === null ? (
						<NewWebhookForm
							workspaceId={workspaceId}
							webhook={webhook}
							urlRef={urlRef}
							onCancel={close}
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
	webhook,
	urlRef,
	onCancel,
	onCreated,
}: {
	workspaceId: string;
	urlRef: RefObject<HTMLInputElement | null>;
	onCancel: () => void;
	webhook?: Webhook | undefined;
	onCreated: (secret: string | null) => void;
}) {
	const create = useMutation(createWebhook);
	const update = useMutation(updateWebhook);
	const mutation = webhook === undefined ? create : update;
	const [url, setUrl] = useState(webhook?.url ?? "");
	const [mode, setMode] = useState<Mode>(webhook !== undefined && webhook.eventTypes.length > 0 ? "choose" : "all");
	const [types, setTypes] = useState<ReadonlySet<string>>(() => new Set(webhook?.eventTypes));

	const target = url.trim();
	const rejected = isUrlRejection(mutation.error);
	const httpsUrl = isHttpsUrl(target);
	const urlError =
		target !== "" && !httpsUrl ? "Use an https:// URL." : rejected ? getErrorMessage(mutation.error) : null;
	const ok = httpsUrl && (mode === "all" || types.size > 0);

	const submit = () => {
		if (!ok || mutation.isPending) return;
		const input = { workspaceId, url: target, eventTypes: mode === "all" ? [] : [...types] };
		if (webhook !== undefined) {
			update.mutate(
				{ ...input, webhookId: webhook.id },
				{
					onSuccess: () => {
						onCreated(null);
					},
				},
			);
		} else {
			create.mutate(input, {
				onSuccess: (res) => {
					onCreated(res.secret);
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
				<p className="m-0 text-[12.5px] text-fg3">
					{webhook === undefined
						? "Loco signs each request following the Standard Webhooks spec and retries failures for up to a day."
						: "Pending retries will use the updated URL."}
				</p>
				<Field label="Endpoint URL" error={urlError}>
					<Input
						ref={urlRef}
						autoComplete="off"
						spellCheck={false}
						value={url}
						placeholder="https://example.com/hooks/loco"
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
				<Button type="button" variant="outline" onClick={onCancel}>
					Cancel
				</Button>
				<Button type="submit" disabled={!ok}>
					{webhook === undefined
						? mutation.isPending
							? "Creating…"
							: "Create webhook"
						: mutation.isPending
							? "Saving…"
							: "Save changes"}
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
	const [opened, setOpened] = useState<string | null>(null);
	const groupChipsRef = useRef(new Map<string, HTMLButtonElement>());
	const open = GROUPS.find((g) => g.label === opened && pageSelection(selected, g.types).some);
	return (
		<div className="flex flex-col gap-2.5">
			<div role="group" aria-label="Event groups" className="flex flex-wrap gap-1.5">
				{GROUPS.map((g) => {
					const state = pageSelection(selected, g.types);
					const count = groupCountLabel(state.count, g.types.length);
					return (
						<ToggleChip
							key={g.label}
							ref={(el) => {
								if (el !== null) groupChipsRef.current.set(g.label, el);
							}}
							pressed={state.some}
							aria-label={`${g.label} ${count}`}
							onPressedChange={(on) => {
								setOpened(g.label);
								if (on || open === g) onChange(setPage(selected, g.types, on));
							}}
						>
							{g.label}
							{state.some && <span className="font-mono text-[11px] opacity-70">{count}</span>}
						</ToggleChip>
					);
				})}
			</div>
			{open !== undefined && (
				<div
					role="group"
					aria-label={`${open.label} events`}
					className="flex flex-wrap items-center gap-1.5 rounded-lg bg-panel px-3 py-2.5"
				>
					<span className="mr-1 text-[12.5px] text-fg3">{open.label}</span>
					{open.types.map((t) => (
						<ToggleChip
							key={t}
							variant="event"
							pressed={selected.has(t)}
							onPressedChange={() => {
								const next = toggleId(selected, t);
								onChange(next);
								if (!pageSelection(next, open.types).some) groupChipsRef.current.get(open.label)?.focus();
							}}
						>
							{t}
						</ToggleChip>
					))}
				</div>
			)}
		</div>
	);
}

export function SigningSecret({ secret, onDone }: { secret: string; onDone: () => void }) {
	return (
		<>
			<DialogBody>
				<p className="m-0 text-[13.5px] text-fg2">
					Copy it now. You won&apos;t see it again. Use it to verify the{" "}
					<code className="font-mono">webhook-signature</code> header.
				</p>
				<div className="flex items-center gap-2.5 rounded-lg bg-panel2 py-2 pr-2 pl-3">
					<code className="min-w-0 flex-1 font-mono text-[13px] break-all">{secret}</code>
					<CopyButton text={secret} name="Copy signing secret" />
				</div>
			</DialogBody>
			<DialogFooter>
				<Button onClick={onDone}>Done</Button>
			</DialogFooter>
		</>
	);
}
