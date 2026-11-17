import { useTimePreferences } from "@/lib/use-time-preferences";
import { createConnectQueryKey, useMutation, useQuery } from "@connectrpc/connect-query";
import type { Webhook } from "@gen/loco/webhook/v1/webhook_pb";
import {
	deleteWebhook,
	listWebhookDeliveries,
	listWebhooks,
} from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useQueryClient } from "@tanstack/react-query";
import { PlusIcon } from "lucide-react";
import { useState, type MouseEvent } from "react";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { ErrorState } from "@/components/design/ErrorState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { parseAsString, useQueryStates } from "nuqs";
import { WebhookActions } from "./WebhookActions";
import { Skeleton } from "@/components/design/Skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { cn } from "@/lib/utils";
import { LimitedAction } from "@/components/design/LimitedAction";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { limitReason, limitUsage } from "@/lib/limits";

import { NewWebhookDialog } from "./NewWebhookDialog";
import { WebhookDrawer } from "./WebhookDrawer";
import { formatDay } from "../settings/parts";
import { DELIVERY_TICKS, eventsSummary, tickStrip, type Tick } from "./webhooks";

const TICK_CLASS: Record<Tick, string> = {
	ok: "bg-ok-glyph opacity-75",
	warn: "bg-warn",
	err: "bg-err",
	wait: "bg-fg4",
};

export function WebhooksPage({
	orgId,
	workspaceId,
	workspaceName,
}: {
	orgId: string;
	workspaceId: string;
	workspaceName: string;
}) {
	const queryClient = useQueryClient();
	const query = useQuery(listWebhooks, { workspaceId });
	const limits = useOrgLimits(orgId);
	const remove = useMutation(deleteWebhook);
	const [{ new: creating, types }, setParams] = useQueryStates({
		new: parseAsString,
		types: parseAsString,
	});
	const [search, setSearch] = useState("");
	const setCreating = (open: boolean) => {
		void setParams({ new: open ? "1" : null, types: open ? types : null });
	};
	const [openId, setOpenId] = useState<string | null>(null);
	const [deleting, setDeleting] = useState<Webhook | null>(null);

	const webhooks = query.data?.webhooks ?? [];
	const filtered = webhooks.filter((w) =>
		`${w.url} ${w.eventTypes.join(" ")}`.toLowerCase().includes(search.toLowerCase()),
	);
	const opened = webhooks.find((w) => w.id === openId) ?? null;
	const listKey = createConnectQueryKey({ schema: listWebhooks, input: { workspaceId }, cardinality: "finite" });

	const confirmDelete = (webhook: Webhook) => {
		remove.mutate(
			{ workspaceId, webhookId: webhook.id },
			{
				onSuccess: () => {
					toast.success("Deleted webhook");
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

	const usage =
		query.data === undefined
			? null
			: limitUsage(webhooks.length, limits?.webhooksPerWorkspace, "webhooks", "workspace");
	const createBlocked = limitReason(usage);

	return (
		<Page className="gap-[22px]">
			<PageHeader
				title="Webhooks"
				actions={
					<LimitedAction reason={createBlocked}>
						<Button
							variant="default"
							disabled={createBlocked !== null}
							onClick={() => {
								setCreating(true);
							}}
						>
							<PlusIcon />
							New webhook
						</Button>
					</LimitedAction>
				}
			/>
			<Section
				search={
					<SearchInput
						aria-label="Search webhooks"
						placeholder="Search by URL or event type"
						value={search}
						onClear={() => {
							setSearch("");
						}}
						onChange={(e) => {
							setSearch(e.target.value);
						}}
					/>
				}
			>
				{query.isPending ? (
					<div className="flex flex-col gap-2 px-5 py-3">
						<Skeleton className="h-9 w-full" />
					</div>
				) : query.error !== null ? (
					<ErrorState error={query.error} what="webhooks" rpc={listWebhooks} onRetry={query.refetch} />
				) : webhooks.length === 0 ? (
					<div className="px-5 py-7 text-fg3">
						No webhooks yet. Create one to get a POST for events in this workspace.
					</div>
				) : filtered.length === 0 ? (
					<div className="px-5 py-7 text-fg3">No webhooks match.</div>
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
							{filtered.map((w) => (
								<WebhookRow
									key={w.id}
									workspaceId={workspaceId}
									workspaceName={workspaceName}
									onChanged={() => {
										void queryClient.invalidateQueries({ queryKey: listKey });
									}}
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
			</Section>

			<WebhookDrawer
				workspaceId={workspaceId}
				workspaceName={workspaceName}
				webhook={opened}
				onDelete={() => {
					setDeleting(opened);
				}}
				onClose={() => {
					setOpenId(null);
				}}
			/>

			<NewWebhookDialog
				workspaceId={workspaceId}
				workspaceName={workspaceName}
				initialTypes={types?.split(",").filter(Boolean) ?? []}
				open={creating === "1"}
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
		</Page>
	);
}

function WebhookRow({
	workspaceId,
	workspaceName,
	webhook,
	onOpen,
	onDelete,
	onChanged,
}: {
	workspaceId: string;
	workspaceName: string;
	webhook: Webhook;
	onOpen: () => void;
	onDelete: () => void;
	onChanged: () => void;
}) {
	const timePref = useTimePreferences();
	const url = new URL(webhook.url);
	const onRowClick = (e: MouseEvent<HTMLTableRowElement>) => {
		const target = e.target;
		if (
			target instanceof Element &&
			e.currentTarget.contains(target) &&
			target.closest("button, [role=menuitem]") === null
		)
			onOpen();
	};
	return (
		<TableRow
			onClick={onRowClick}
			tabIndex={0}
			aria-label={`Deliveries for ${webhook.url}`}
			onKeyDown={(e) => {
				if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
					e.preventDefault();
					onOpen();
				}
			}}
		>
			<TableCell className="w-full max-w-0">
				<button
					type="button"
					onClick={onOpen}
					aria-label={`Deliveries for ${webhook.url}`}
					title={webhook.url}
					className="block w-full text-left font-mono"
				>
					<span className="block truncate text-[13px]">{url.host}</span>
					<span className="block truncate text-[11.5px] text-fg3">
						{url.pathname}
						{url.search}
					</span>
				</button>
			</TableCell>
			<TableCell className="hidden text-[12.5px] whitespace-nowrap text-fg2 md:table-cell">
				{eventsSummary(webhook.eventTypes)}
			</TableCell>
			<TableCell className="max-w-[180px]">
				<DeliveryTicks workspaceId={workspaceId} webhookId={webhook.id} />
			</TableCell>
			<TableCell className="hidden font-mono text-[12px] whitespace-nowrap text-fg3 md:table-cell">
				{formatDay(webhook.createdAt, timePref)}
			</TableCell>
			<TableCell>
				<WebhookActions
					menuOnly
					workspaceId={workspaceId}
					workspaceName={workspaceName}
					webhook={webhook}
					onDelete={onDelete}
					onChanged={onChanged}
				/>
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
	const errors = Number(query.data.failingCount);
	const label = `${errors} failing`;
	return (
		<span className="flex min-w-0 items-center gap-2.5">
			<span
				role="img"
				aria-label={`${ticks.length.toString()} recent deliveries, ${label} in total`}
				className="flex min-w-0 items-center gap-0.5 overflow-hidden"
			>
				{ticks.map((t) => (
					<i
						key={t.id}
						title={t.tip}
						className={cn("block h-4 min-w-0.5 flex-[0_1_4px] rounded-[1.5px]", TICK_CLASS[t.tick])}
					/>
				))}
			</span>
			{errors > 0 && (
				<span title={label} className="min-w-0 truncate text-[12px] text-warn-fg">
					{label}
				</span>
			)}
		</span>
	);
}
