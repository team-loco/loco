import { useMutation } from "@connectrpc/connect-query";
import { createWebhook } from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { CopyButton } from "@/components/design/CopyButton";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage } from "@/lib/error-handler";
import { pageSelection, toggleId, togglePage } from "@/lib/selection";

import { isUrlRejection, webhookEventGroups, type EventGroup } from "./webhooks";

type Mode = "all" | "choose";

const GROUPS = webhookEventGroups();

export function NewWebhookDialog({
	workspaceId,
	workspaceName,
	open,
	onClose,
	onCreated,
}: {
	workspaceId: string;
	workspaceName: string;
	open: boolean;
	onClose: () => void;
	onCreated: () => void;
}) {
	const [secret, setSecret] = useState<string | null>(null);
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
			{open &&
				(secret === null ? (
					<NewWebhookForm
						workspaceId={workspaceId}
						workspaceName={workspaceName}
						onCancel={close}
						onCreated={(s) => {
							setSecret(s);
							onCreated();
						}}
					/>
				) : (
					<SigningSecret secret={secret} onDone={close} />
				))}
		</Dialog>
	);
}

function NewWebhookForm({
	workspaceId,
	workspaceName,
	onCancel,
	onCreated,
}: {
	workspaceId: string;
	workspaceName: string;
	onCancel: () => void;
	onCreated: (secret: string) => void;
}) {
	const create = useMutation(createWebhook);
	const [url, setUrl] = useState("");
	const [mode, setMode] = useState<Mode>("all");
	const [types, setTypes] = useState<ReadonlySet<string>>(() => new Set());

	const target = url.trim();
	const rejected = isUrlRejection(create.error);
	const urlError = rejected ? getErrorMessage(create.error) : null;
	const ok = target !== "" && (mode === "all" || types.size > 0) && !create.isPending;

	const submit = () => {
		if (!ok) return;
		create.mutate(
			{ workspaceId, url: target, eventTypes: mode === "all" ? [] : [...types] },
			{
				onSuccess: (res) => {
					onCreated(res.secret);
				},
			},
		);
	};

	return (
		<DialogContent title={`New webhook in ${workspaceName}`} className="w-[560px]">
			<form
				className="contents"
				onSubmit={(e) => {
					e.preventDefault();
					submit();
				}}
			>
				<DialogBody>
					<p className="m-0 text-[12.5px] text-fg3">
						Loco signs each request following the Standard Webhooks spec and retries failures for up to a day.
					</p>
					<Field label="Endpoint URL" error={urlError}>
						<Input
							autoFocus
							autoComplete="off"
							spellCheck={false}
							value={url}
							placeholder="https://example.com/hooks/loco"
							aria-invalid={urlError !== null}
							className="h-9 font-mono text-[13.5px]"
							onChange={(e) => {
								setUrl(e.target.value);
								create.reset();
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
						<div className="grid max-h-[300px] grid-cols-1 gap-x-5 gap-y-3.5 overflow-y-auto rounded-lg bg-panel p-3 sm:grid-cols-2">
							{GROUPS.map((g) => (
								<GroupPicker
									key={g.label}
									group={g}
									selected={types}
									onChange={(next) => {
										setTypes(next);
										create.reset();
									}}
								/>
							))}
						</div>
					)}
					{create.error !== null && !rejected && (
						<span className="text-[12.5px] text-bad-fg">
							{getErrorMessage(create.error, "Could not add the webhook")}
						</span>
					)}
				</DialogBody>
				<DialogFooter>
					<Button type="button" variant="outline" onClick={onCancel}>
						Cancel
					</Button>
					<Button type="submit" disabled={!ok}>
						{create.isPending ? "Creating…" : "Create webhook"}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}

const checkbox = "m-0 size-3.5 shrink-0 accent-foreground";

function GroupPicker({
	group,
	selected,
	onChange,
}: {
	group: EventGroup;
	selected: ReadonlySet<string>;
	onChange: (next: ReadonlySet<string>) => void;
}) {
	const state = pageSelection(selected, group.types);
	return (
		<div role="group" aria-label={group.label} className="flex min-w-0 flex-col gap-1.5">
			<label className="flex items-center gap-2 text-[13px] font-medium">
				<input
					type="checkbox"
					className={checkbox}
					checked={state.all}
					ref={(el) => {
						if (el !== null) el.indeterminate = state.some && !state.all;
					}}
					onChange={() => {
						onChange(togglePage(selected, group.types));
					}}
				/>
				{group.label}
			</label>
			<div className="flex flex-col gap-1 pl-[22px]">
				{group.types.map((t) => (
					<label key={t} className="flex min-w-0 items-center gap-2 font-mono text-[12px] text-fg2">
						<input
							type="checkbox"
							className={checkbox}
							checked={selected.has(t)}
							onChange={() => {
								onChange(toggleId(selected, t));
							}}
						/>
						<span className="truncate">{t}</span>
					</label>
				))}
			</div>
		</div>
	);
}

function SigningSecret({ secret, onDone }: { secret: string; onDone: () => void }) {
	return (
		<DialogContent title="Signing secret">
			<DialogBody>
				<p className="m-0 text-[13.5px] text-fg2">
					Copy it now. You won&apos;t see it again. Use it to verify the{" "}
					<code className="font-mono">webhook-signature</code> header.
				</p>
				<div className="flex items-center gap-2.5 rounded-lg bg-panel2 py-2 pr-2 pl-3">
					<code className="min-w-0 flex-1 font-mono text-[12.5px] [overflow-wrap:anywhere]">{secret}</code>
					<CopyButton text={secret} name="Copy signing secret" />
				</div>
			</DialogBody>
			<DialogFooter>
				<Button onClick={onDone}>Done</Button>
			</DialogFooter>
		</DialogContent>
	);
}
