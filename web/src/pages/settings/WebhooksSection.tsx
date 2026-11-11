import { createConnectQueryKey, useMutation, useQuery } from "@connectrpc/connect-query";
import type { Webhook } from "@gen/loco/webhook/v1/webhook_pb";
import {
	deleteWebhook,
	listWebhookDeliveries,
	listWebhooks,
} from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useQueryClient } from "@tanstack/react-query";
import { CopyIcon, HistoryIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState, type MouseEvent } from "react";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { RowMenu } from "@/components/design/RowMenu";
import { Skeleton } from "@/components/design/Skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { NewWebhookDialog } from "./NewWebhookDialog";
import { WebhookDrawer } from "./WebhookDrawer";
import { formatDay, SettingsCard, SettingsSection } from "./parts";
import { DELIVERY_TICKS, eventsSummary, failedCount, tickStrip, type Tick } from "./webhooks";

const TICK_CLASS: Record<Tick, string> = {
	ok: "bg-ok-glyph opacity-75",
	warn: "bg-warn",
	err: "bg-err",
};

export function WebhooksSection({ workspaceId, workspaceName }: { workspaceId: string; workspaceName: string }) {
	const queryClient = useQueryClient();
	const query = useQuery(listWebhooks, { workspaceId });
	const remove = useMutation(deleteWebhook);
	const [creating, setCreating] = useState(false);
	const [openId, setOpenId] = useState<string | null>(null);
	const [deleting, setDeleting] = useState<Webhook | null>(null);

	const webhooks = query.data?.webhooks ?? [];
	const opened = webhooks.find((w) => w.id === openId) ?? null;
	const listKey = createConnectQueryKey({ schema: listWebhooks, input: { workspaceId }, cardinality: "finite" });

	const confirmDelete = (webhook: Webhook) => {
		remove.mutate(
			{ workspaceId, webhookId: webhook.id },
			{
				onSuccess: () => {
					toast.success("Webhook deleted");
					setDeleting(null);
					if (openId === webhook.id) setOpenId(null);
					void queryClient.invalidateQueries({ queryKey: listKey });
				},
				onError: (err) => {
					toastConnectError(err, "Could not delete the webhook");
				},
			},
		);
	};

	return (
		<SettingsSection
			title="Webhooks"
			count={query.data === undefined ? undefined : webhooks.length}
			actions={
				<Button
					variant="outline"
					size="sm"
					onClick={() => {
						setCreating(true);
					}}
				>
					<PlusIcon />
					New webhook
				</Button>
			}
		>
			<SettingsCard className="pt-1">
				{query.isPending ? (
					<div className="flex flex-col gap-2 px-5 py-3">
						<Skeleton className="h-9 w-full" />
					</div>
				) : query.error !== null ? (
					<div className="px-5 py-7 text-fg3">{getErrorMessage(query.error, "Failed to load webhooks")}</div>
				) : webhooks.length === 0 ? (
					<div className="px-5 py-7 text-fg3">No webhooks yet.</div>
				) : (
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Endpoint</TableHead>
								<TableHead className="hidden md:table-cell">Events</TableHead>
								<TableHead>Last {DELIVERY_TICKS} deliveries</TableHead>
								<TableHead className="hidden md:table-cell">Created</TableHead>
								<TableHead className="w-10">
									<span className="sr-only">Actions</span>
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{webhooks.map((w) => (
								<WebhookRow
									key={w.id}
									workspaceId={workspaceId}
									webhook={w}
									onOpen={() => {
										setOpenId(w.id);
									}}
									onDelete={() => {
										setDeleting(w);
									}}
								/>
							))}
						</TableBody>
					</Table>
				)}
			</SettingsCard>

			<WebhookDrawer
				workspaceId={workspaceId}
				workspaceName={workspaceName}
				webhook={opened}
				onClose={() => {
					setOpenId(null);
				}}
			/>

			<NewWebhookDialog
				workspaceId={workspaceId}
				workspaceName={workspaceName}
				open={creating}
				onClose={() => {
					setCreating(false);
				}}
				onCreated={() => {
					void queryClient.invalidateQueries({ queryKey: listKey });
				}}
			/>

			<ConfirmDialog
				open={deleting !== null}
				title="Delete webhook?"
				description={
					<>
						Deliveries to <span className="font-mono [overflow-wrap:anywhere]">{deleting?.url}</span> stop right away.
					</>
				}
				confirmLabel="Delete webhook"
				pendingLabel="Deleting…"
				pending={remove.isPending}
				onConfirm={() => {
					if (deleting !== null) confirmDelete(deleting);
				}}
				onClose={() => {
					setDeleting(null);
				}}
			/>
		</SettingsSection>
	);
}

function WebhookRow({
	workspaceId,
	webhook,
	onOpen,
	onDelete,
}: {
	workspaceId: string;
	webhook: Webhook;
	onOpen: () => void;
	onDelete: () => void;
}) {
	const onRowClick = (e: MouseEvent<HTMLTableRowElement>) => {
		const target = e.target;
		if (!(target instanceof Element) || !e.currentTarget.contains(target) || target.closest("button") !== null) return;
		onOpen();
	};
	const copyUrl = () => {
		void navigator.clipboard.writeText(webhook.url).then(() => {
			toast.success("Copied URL");
		});
	};

	return (
		<TableRow className="hover:bg-panel2" onClick={onRowClick}>
			<TableCell className="w-full max-w-0">
				<button
					type="button"
					className="block max-w-full truncate rounded-sm text-left font-mono text-[12.5px] hover:underline"
					aria-label={`Deliveries for ${webhook.url}`}
					onClick={onOpen}
				>
					{webhook.url}
				</button>
			</TableCell>
			<TableCell className="hidden text-[12.5px] whitespace-nowrap text-fg2 md:table-cell">
				{eventsSummary(webhook.eventTypes)}
			</TableCell>
			<TableCell>
				<DeliveryTicks workspaceId={workspaceId} webhookId={webhook.id} />
			</TableCell>
			<TableCell className="hidden font-mono text-[12px] whitespace-nowrap text-fg3 md:table-cell">
				{formatDay(webhook.createdAt)}
			</TableCell>
			<TableCell className="text-right">
				<RowMenu label={`Actions for ${webhook.url}`}>
					<DropdownMenuItem onClick={onOpen}>
						<HistoryIcon />
						Deliveries
					</DropdownMenuItem>
					<DropdownMenuItem onClick={copyUrl}>
						<CopyIcon />
						Copy URL
					</DropdownMenuItem>
					<DropdownMenuSeparator />
					<DropdownMenuItem variant="destructive" onClick={onDelete}>
						<Trash2Icon />
						Delete
					</DropdownMenuItem>
				</RowMenu>
			</TableCell>
		</TableRow>
	);
}

function DeliveryTicks({ workspaceId, webhookId }: { workspaceId: string; webhookId: string }) {
	const query = useQuery(listWebhookDeliveries, { workspaceId, webhookId });
	if (query.isPending) return <Skeleton className="h-4 w-36" />;
	if (query.error !== null) {
		return <span className="text-[12px] text-bad-fg">{getErrorMessage(query.error, "Failed to load deliveries")}</span>;
	}
	const ticks = tickStrip(query.data.deliveries);
	if (ticks.length === 0) return <span className="text-[12px] text-fg3">No deliveries yet</span>;
	const failed = failedCount(query.data.deliveries.slice(0, DELIVERY_TICKS));
	const label = `${failed.toString()} failed`;
	return (
		<span className="flex min-w-0 items-center gap-2.5">
			<span
				role="img"
				aria-label={`${ticks.length.toString()} recent deliveries, ${label}`}
				className="flex items-center gap-0.5"
			>
				{ticks.map((t) => (
					<i key={t.id} title={t.tip} className={cn("block h-4 w-1 rounded-[1.5px]", TICK_CLASS[t.tick])} />
				))}
			</span>
			{failed > 0 && <span className="text-[12px] whitespace-nowrap text-warn-fg">{label}</span>}
		</span>
	);
}
