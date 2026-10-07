import { useMutation, useQuery } from "@connectrpc/connect-query";
import type { Webhook, WebhookDelivery } from "@gen/loco/webhook/v1/webhook_pb";
import {
	createWebhook,
	deleteWebhook,
	listWebhookDeliveries,
	listWebhooks,
} from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronDownIcon, ChevronRightIcon, Trash2Icon, WebhookIcon } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useCopy } from "@/hooks/useCopy";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { formatClock, maybeTsMs } from "@/lib/time";

import { AUDIT_CATEGORIES, type AuditCategory, auditLabel, typesIn } from "./auditEvents";
import { CopyField, formatDay, SettingsCard } from "./parts";

const CATEGORIES = AUDIT_CATEGORIES.filter(
	(c): c is { value: AuditCategory; label: string } => c.value !== "all" && c.value !== "organization",
);

function isCategory(value: string): value is AuditCategory {
	return CATEGORIES.some((c) => c.value === value);
}

function eventsLabel(types: string[]): string {
	if (types.length === 0) return "All events";
	const names = CATEGORIES.filter((c) => typesIn(c.value).every((t) => types.includes(t))).map((c) => c.label);
	return names.length > 0 ? names.join(", ") : `${types.length.toString()} event types`;
}

function deliveryTone(status: string): "ok" | "warn" | "bad" {
	switch (status) {
		case "succeeded": {
			return "ok";
		}
		case "failed": {
			return "bad";
		}
		default: {
			return "warn";
		}
	}
}

function when(delivery: WebhookDelivery): string {
	const ms = maybeTsMs(delivery.createdAt);
	return ms === undefined ? "—" : `${formatDay(delivery.createdAt)} · ${formatClock(ms)}`;
}

function Deliveries({ workspaceId, webhookId }: { workspaceId: string; webhookId: string }) {
	const query = useQuery(listWebhookDeliveries, { workspaceId, webhookId });
	const deliveries = query.data?.deliveries ?? [];
	if (query.isLoading) return <Skeleton className="h-8 w-full" />;
	if (query.error !== null) {
		return <span className="text-sm text-bad-fg">{getErrorMessage(query.error, "Failed to load deliveries")}</span>;
	}
	if (deliveries.length === 0) {
		return <span className="text-[12.5px] text-fg3">No deliveries yet. Events appear here as they are sent.</span>;
	}
	return (
		<div className="flex flex-col rounded-sm border border-line">
			{deliveries.map((d) => (
				<div
					key={d.id}
					className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-3 py-2 text-[12.5px] last:border-b-0"
				>
					<Badge tone={deliveryTone(d.status)} size="sm">
						{d.status}
					</Badge>
					<span className="min-w-0 flex-1 basis-40 truncate">{auditLabel(d.eventType)}</span>
					<span className="flex gap-3 text-fg3 tabular-nums">
						<span className="font-mono">
							{d.lastStatusCode > 0 ? d.lastStatusCode.toString() : "—"} · {d.attempts.toString()}{" "}
							{d.attempts === 1 ? "attempt" : "attempts"}
						</span>
						<span>{when(d)}</span>
					</span>
					{d.lastError !== "" && d.status !== "succeeded" && (
						<span className="w-full truncate font-mono text-[12px] text-fg3">{d.lastError}</span>
					)}
				</div>
			))}
		</div>
	);
}

function WebhookRow({ workspaceId, webhook }: { workspaceId: string; webhook: Webhook }) {
	const queryClient = useQueryClient();
	const remove = useMutation(deleteWebhook);
	const [open, setOpen] = useState(false);

	const onRemove = () => {
		remove.mutate(
			{ workspaceId, webhookId: webhook.id },
			{
				onSuccess: () => {
					toast.success("Webhook removed");
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
					toastConnectError(err, "Could not remove the webhook");
				},
			},
		);
	};

	return (
		<div className="flex flex-col gap-3 border-b border-line px-5 py-4 last:border-b-0">
			<div className="flex items-center gap-2.5">
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label={open ? "Hide deliveries" : "Show deliveries"}
					aria-expanded={open}
					onClick={() => {
						setOpen(!open);
					}}
				>
					{open ? <ChevronDownIcon /> : <ChevronRightIcon />}
				</Button>
				<div className="flex min-w-0 flex-1 flex-col">
					<code className="truncate font-mono text-[13px]">{webhook.url}</code>
					<span className="text-[12px] text-fg3">{eventsLabel(webhook.eventTypes)}</span>
				</div>
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label={`Remove ${webhook.url}`}
					disabled={remove.isPending}
					onClick={onRemove}
				>
					<Trash2Icon />
				</Button>
			</div>
			{open && <Deliveries workspaceId={workspaceId} webhookId={webhook.id} />}
		</div>
	);
}

function NewSecret({ secret, onDone }: { secret: string; onDone: () => void }) {
	const [copied, copy] = useCopy();
	return (
		<div className="flex flex-col gap-2.5 border-t border-line bg-bg2 px-5 py-3.5">
			<span className="text-[12.5px] text-fg2">
				Copy this signing secret now. Loco won&apos;t show it again. Use it to verify the{" "}
				<code className="font-mono">webhook-signature</code> header.
			</span>
			<CopyField
				label="Signing secret"
				value={secret}
				copied={copied === "secret"}
				onCopy={() => {
					copy("secret", secret);
				}}
			/>
			<div>
				<Button variant="outline" className="h-[30px] px-2.5" onClick={onDone}>
					Done
				</Button>
			</div>
		</div>
	);
}

function AddWebhook({ workspaceId, onCreated }: { workspaceId: string; onCreated: (secret: string) => void }) {
	const queryClient = useQueryClient();
	const create = useMutation(createWebhook);
	const [url, setUrl] = useState("");
	const [categories, setCategories] = useState<AuditCategory[]>([]);

	const submit = (event: FormEvent) => {
		event.preventDefault();
		const target = url.trim();
		if (target === "") return;
		create.mutate(
			{ workspaceId, url: target, eventTypes: categories.flatMap((c) => typesIn(c)) },
			{
				onSuccess: (res) => {
					setUrl("");
					setCategories([]);
					onCreated(res.secret);
					void queryClient.invalidateQueries();
				},
			},
		);
	};

	return (
		<form className="flex flex-col gap-2.5 border-t border-line bg-bg2 px-5 py-3.5" onSubmit={submit}>
			<Input
				value={url}
				placeholder="https://example.com/loco-events"
				aria-label="Webhook URL"
				className="h-[30px] text-[13.5px]"
				onChange={(e) => {
					setUrl(e.target.value);
					create.reset();
				}}
			/>
			<div className="flex flex-wrap items-center gap-3">
				<span className="text-[12.5px] text-fg3">
					{categories.length === 0 ? "Sends all events. Pick categories to narrow it:" : "Sends events in"}
				</span>
				<ToggleGroup
					variant="segmented"
					multiple
					className="max-w-full flex-wrap"
					value={categories}
					onValueChange={(v: string[]) => {
						setCategories(v.filter(isCategory));
					}}
				>
					{CATEGORIES.map((c) => (
						<ToggleGroupItem key={c.value} value={c.value} className="h-7! px-3 text-[12.5px]">
							{c.label}
						</ToggleGroupItem>
					))}
				</ToggleGroup>
			</div>
			{create.error !== null && (
				<span className="text-sm text-bad-fg">{getErrorMessage(create.error, "Could not add the webhook")}</span>
			)}
			<div>
				<Button type="submit" variant="outline" className="h-[30px] px-2.5" disabled={create.isPending || url.trim() === ""}>
					Add webhook
				</Button>
			</div>
		</form>
	);
}

export function WebhooksCard({ workspaceId }: { workspaceId: string }) {
	const webhooksQuery = useQuery(listWebhooks, { workspaceId });
	const webhooks = webhooksQuery.data?.webhooks ?? [];
	const [secret, setSecret] = useState<string | null>(null);

	return (
		<SettingsCard>
			<div className="flex items-start gap-2.5 border-b border-line px-5 py-3.5">
				<WebhookIcon className="mt-1 size-[15px] shrink-0 text-fg3" />
				<div className="flex min-w-0 flex-col gap-1">
					<span className="font-semibold">Webhooks</span>
					<span className="text-[12.5px] text-fg3">
						Send this workspace&apos;s events to your own endpoint. Loco signs each request with the endpoint&apos;s
						secret, following the Standard Webhooks spec, and retries failures for up to a day.
					</span>
				</div>
			</div>
			{webhooksQuery.isLoading && (
				<div className="px-5 py-3">
					<Skeleton className="h-9 w-full" />
				</div>
			)}
			{webhooksQuery.error !== null && (
				<div className="px-5 py-5 text-fg3">{getErrorMessage(webhooksQuery.error, "Failed to load webhooks")}</div>
			)}
			{webhooks.map((w) => (
				<WebhookRow key={w.id} workspaceId={workspaceId} webhook={w} />
			))}
			{secret === null ? (
				<AddWebhook workspaceId={workspaceId} onCreated={setSecret} />
			) : (
				<NewSecret
					secret={secret}
					onDone={() => {
						setSecret(null);
					}}
				/>
			)}
		</SettingsCard>
	);
}
