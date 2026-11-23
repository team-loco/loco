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
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { ErrorState } from "@/components/design/ErrorState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { parseAsString, useQueryStates } from "nuqs";
import { WebhookActions } from "./WebhookActions";
import { Skeleton } from "@/components/design/Skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { useTokenPage } from "@/hooks/useTokenPage";
import { formatTotal } from "@/lib/paging";
import { cn } from "@/lib/utils";
import { LimitCount, LimitedAction } from "@/components/design/LimitedAction";
import { DocsButton } from "@/components/design/DocsButton";
import { EmptyHero } from "@/components/design/EmptyHero";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { limitReason, limitUsage } from "@/lib/limits";

import { NewWebhookDialog } from "./NewWebhookDialog";
import { WebhookDrawer } from "./WebhookDrawer";
import { formatDay } from "../settings/parts";
import { DOCS_URL } from "../splash/links";
import { WebhooksArt } from "./WebhooksArt";
import { DELIVERY_TICKS, eventsSummary, hasPending, refreshDeliveries, tickStrip, type Tick } from "./webhooks";

const PENDING_REFRESH_MS = 5_000;
const WEBHOOK_DOCS = `${DOCS_URL}/operations/webhooks/`;

const TICK_CLASS: Record<Tick, string> = {
	ok: "bg-ok-glyph opacity-75",
	warn: "bg-warn",
	err: "bg-err",
	off: "bg-fg4",
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
	const [{ new: creating, types, hook: openId }, setParams] = useQueryStates({
		new: parseAsString,
		types: parseAsString,
		hook: parseAsString,
	});
	const [search, setSearch] = useState("");
	const setCreating = (open: boolean) => {
		void setParams({ new: open ? "1" : null, types: open ? types : null }, { history: "replace" });
	};
	const setOpenId = (id: string | null) => {
		void setParams({ hook: id }, { history: "replace" });
	};
	const [deleting, setDeleting] = useState<Webhook | null>(null);

	const webhooks = query.data?.webhooks ?? [];
	const filtered = webhooks.filter((w) =>
		`${w.name} ${w.url} ${w.eventTypes.join(" ")}`.toLowerCase().includes(search.toLowerCase()),
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

	const onChanged = () => {
		void queryClient.invalidateQueries({ queryKey: listKey });
		void refreshDeliveries(queryClient);
	};
	const table = useDataTable("webhooks", {
		columns: webhookColumns({
			workspaceId,
			workspaceName,
			onOpen: setOpenId,
			onDelete: setDeleting,
			onChanged,
		}),
		data: filtered,
		getRowId: (w) => w.id,
	});

	const usage =
		query.data === undefined
			? null
			: limitUsage(webhooks.length, limits?.webhooksPerWorkspace, "webhooks", "workspace");
	const createBlocked = limitReason(usage);
	const newWebhook = (
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
	);

	return (
		<Page className="gap-[22px]">
			<PageHeader
				title={
					<>
						Webhooks
						<LimitCount usage={usage} />
					</>
				}
				actions={newWebhook}
			/>
			{query.data !== undefined && webhooks.length === 0 ? (
				<EmptyHero
					art={<WebhooksArt />}
					title="No webhooks yet"
					subtitle={`Loco sends a signed POST to your URL when something happens in ${workspaceName}, such as a build finishing, a resource scaling or a secret changing.`}
				>
					<div className="mt-1.5 flex flex-wrap justify-center gap-2">
						{newWebhook}
						<DocsButton href={WEBHOOK_DOCS} />
					</div>
				</EmptyHero>
			) : (
				<Section
					search={
						<SearchInput
							aria-label="Search webhooks"
							placeholder="Search by name, URL or event type"
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
					) : filtered.length === 0 ? (
						<div className="px-5 py-7 text-center text-[13px] text-fg3">No webhooks match.</div>
					) : (
						<DataTable
							table={table}
							label="Webhooks"
							activeRowId={openId}
							onRowClick={(row) => {
								setOpenId(row.id);
							}}
						/>
					)}
				</Section>
			)}

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

function Created({ webhook }: { webhook: Webhook }) {
	const timePref = useTimePreferences();
	return <>{formatDay(webhook.createdAt, timePref)}</>;
}

function webhookColumns({
	workspaceId,
	workspaceName,
	onOpen,
	onDelete,
	onChanged,
}: {
	workspaceId: string;
	workspaceName: string;
	onOpen: (id: string) => void;
	onDelete: (webhook: Webhook) => void;
	onChanged: () => void;
}): DataColumn<Webhook>[] {
	return [
		{
			id: "endpoint",
			header: "Endpoint",
			size: 160,
			minSize: 140,
			meta: { grow: true },
			cell: ({ row: { original: webhook } }) => {
				const url = new URL(webhook.url);
				return (
					<Tooltip>
						<TooltipTrigger
							render={
								<button
									type="button"
									aria-label={`Deliveries for ${webhook.url}`}
									className="block w-full min-w-0 text-left font-mono"
									onClick={() => {
										onOpen(webhook.id);
									}}
								/>
							}
						>
							<span className={cn("block truncate text-[13px]", webhook.name !== "" && "font-sans")}>
								{webhook.name === "" ? url.host : webhook.name}
							</span>
							<span className="block truncate text-[11.5px] text-fg3">
								{`${webhook.name === "" ? "" : url.host}${url.pathname}${url.search}`}
							</span>
						</TooltipTrigger>
						<TooltipContent className="font-mono break-all">{webhook.url}</TooltipContent>
					</Tooltip>
				);
			},
		},
		{
			id: "events",
			header: "Events",
			size: 130,
			minSize: 90,
			meta: { hideOnMobile: true, fit: true, cellClassName: "text-[12.5px] whitespace-nowrap text-fg2" },
			cell: ({ row: { original: webhook } }) => <CellText text={eventsSummary(webhook.eventTypes)} />,
		},
		{
			id: "deliveries",
			header: `Last ${DELIVERY_TICKS.toString()} deliveries`,
			size: 140,
			minSize: 120,
			cell: ({ row: { original: webhook } }) => <DeliveryTicks workspaceId={workspaceId} webhookId={webhook.id} />,
		},
		{
			id: "created",
			header: "Created",
			size: 130,
			minSize: 90,
			meta: { hideOnMobile: true, fit: true, cellClassName: "font-mono text-[12px] whitespace-nowrap text-fg3" },
			cell: ({ row: { original: webhook } }) => <Created webhook={webhook} />,
		},
		{
			id: "menu",
			meta: { pinEnd: true, label: "Actions" },
			cell: ({ row: { original: webhook } }) => (
				<WebhookActions
					menuOnly
					workspaceId={workspaceId}
					workspaceName={workspaceName}
					webhook={webhook}
					onDelete={() => {
						onDelete(webhook);
					}}
					onChanged={onChanged}
				/>
			),
		},
	];
}

const NO_TOKENS: string[] = [];

function DeliveryTicks({ workspaceId, webhookId }: { workspaceId: string; webhookId: string }) {
	const { query, counts } = useTokenPage(listWebhookDeliveries, { workspaceId, webhookId }, NO_TOKENS, {
		refetchInterval: (q) => (hasPending(q.state.data?.deliveries) ? PENDING_REFRESH_MS : false),
	});
	if (query.isPending) return <Skeleton className="h-4 w-36" />;
	if (query.error !== null) {
		return <span className="text-[12px] text-bad-fg">{getErrorMessage(query.error, "Failed to load deliveries")}</span>;
	}
	const ticks = tickStrip(query.data.deliveries);
	if (ticks.length === 0) return <span className="text-[12px] text-fg3">No deliveries yet</span>;
	const errors = counts?.failingCount ?? 0;
	const label = `${formatTotal(errors)} failing`;
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
