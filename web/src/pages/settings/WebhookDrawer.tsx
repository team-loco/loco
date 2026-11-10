import { createConnectQueryKey, useInfiniteQuery, useMutation } from "@connectrpc/connect-query";
import type { Webhook, WebhookDelivery } from "@gen/loco/webhook/v1/webhook_pb";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
	listWebhooks,
	redeliverWebhookDelivery,
	listWebhookDeliveries,
} from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { Badge } from "@/components/design/Badge";
import { Pager } from "@/components/design/Pager";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { nextPage, pagedRows, type Paging } from "@/lib/paging";
import { formatClock, tsMs } from "@/lib/time";

import { WebhookActions } from "./WebhookActions";
import { formatDay } from "./parts";
import {
	deliveryDetail,
	deliveryLabel,
	deliveryTick,
	deliveryWhen,
	eventsSummary,
	STATUS_FAILED,
	STATUS_PENDING,
	STATUS_SUCCEEDED,
} from "./webhooks";

const PAGE_SIZE = 10;
const PAGE_SIZES = [PAGE_SIZE];
const FIRST_PAGE: Paging = { page: 0, size: PAGE_SIZE };
const SKELETON_ROWS = [0, 1, 2];

const FILTERS = [
	{ value: "", label: "All" },
	{ value: STATUS_PENDING, label: "Pending" },
	{ value: STATUS_SUCCEEDED, label: "Delivered" },
	{ value: STATUS_FAILED, label: "Failed" },
] as const;
type Filter = (typeof FILTERS)[number]["value"];

export function WebhookDrawer({
	workspaceId,
	workspaceName,
	webhook,
	onClose,
}: {
	workspaceId: string;
	workspaceName: string;
	webhook: Webhook | null;
	onClose: () => void;
}) {
	return (
		<Sheet
			open={webhook !== null}
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
		>
			<SheetContent size="wide">
				{webhook !== null && (
					<WebhookDetail key={webhook.id} workspaceId={workspaceId} workspaceName={workspaceName} webhook={webhook} />
				)}
			</SheetContent>
		</Sheet>
	);
}

function WebhookDetail({
	workspaceId,
	workspaceName,
	webhook,
}: {
	workspaceId: string;
	workspaceName: string;
	webhook: Webhook;
}) {
	const [filter, setFilter] = useState<Filter>("");
	const [paging, setPaging] = useState(FIRST_PAGE);
	const queryClient = useQueryClient();
	const query = useInfiniteQuery(
		listWebhookDeliveries,
		{ workspaceId, webhookId: webhook.id, status: filter, pageSize: PAGE_SIZE, pageToken: "" },
		{
			pageParamKey: "pageToken",
			getNextPageParam: (last) => last.nextPageToken || undefined,
			refetchInterval: 15_000,
		},
	);
	const loaded = query.data?.pages.flatMap((p) => p.deliveries) ?? [];
	const paged = pagedRows(loaded, paging, setPaging, PAGE_SIZES);
	const next = nextPage(
		paged,
		query.isFetching,
		query.hasNextPage,
		async () => !(await query.fetchNextPage()).isError,
		setPaging,
	);
	const refresh = () => {
		void queryClient.invalidateQueries({
			queryKey: createConnectQueryKey({ schema: listWebhookDeliveries, cardinality: undefined }),
		});
		void queryClient.invalidateQueries({
			queryKey: createConnectQueryKey({ schema: listWebhooks, cardinality: "finite" }),
		});
	};
	const redeliver = useMutation(redeliverWebhookDelivery, {
		onSuccess: () => {
			toast.success("Redelivery queued");
			refresh();
		},
		onError: (error) => {
			toastConnectError(error, "Could not redeliver the event");
		},
	});
	const chips = webhook.eventTypes.length === 0 ? [`Every event in ${workspaceName}`] : webhook.eventTypes;

	return (
		<>
			<SheetHeader>
				<div className="flex min-w-0 flex-col gap-0.5">
					<SheetTitle className="truncate font-mono text-[13.5px] font-medium tracking-normal">
						{webhook.url}
					</SheetTitle>
					<SheetDescription className="text-[12px]">
						{eventsSummary(webhook.eventTypes)} · created {formatDay(webhook.createdAt)}
					</SheetDescription>
				</div>
			</SheetHeader>
			<SheetBody>
				<WebhookActions workspaceId={workspaceId} workspaceName={workspaceName} webhook={webhook} onChanged={refresh} />
				<ul aria-label="Events" className="m-0 flex list-none flex-wrap gap-1.5 p-0">
					{chips.map((c) => (
						<li key={c}>
							<Badge tone="muted" size="sm" className="font-mono font-normal">
								{c}
							</Badge>
						</li>
					))}
				</ul>
				<div className="flex flex-wrap items-center justify-between gap-2.5">
					<h3 className="m-0 text-[14px] font-medium">Deliveries</h3>
					<ToggleGroup
						variant="segmented"
						aria-label="Show deliveries"
						value={[filter]}
						onValueChange={(v: string[]) => {
							const selected = FILTERS.find((option) => option.value === v[0]);
							if (selected === undefined) return;
							setFilter(selected.value);
							setPaging(FIRST_PAGE);
						}}
					>
						{FILTERS.map((f) => (
							<ToggleGroupItem key={f.value} value={f.value}>
								{f.label}
							</ToggleGroupItem>
						))}
					</ToggleGroup>
				</div>
				<DeliveryList
					deliveries={paged.rows}
					isLoading={query.isPending}
					error={query.isFetchNextPageError ? null : query.error}
					onRedeliver={(deliveryId) => {
						redeliver.mutate({ workspaceId, webhookId: webhook.id, deliveryId });
					}}
					redelivering={redeliver.isPending}
					empty={
						filter === ""
							? "No deliveries yet. Events appear here as they are sent."
							: "No deliveries with this status."
					}
				/>
				{query.error !== null && (
					<Button
						variant="outline"
						disabled={query.isFetching}
						onClick={() => {
							if (query.isFetchNextPageError) next?.();
							else void query.refetch();
						}}
					>
						Retry deliveries
					</Button>
				)}
				{loaded.length > 0 && (
					<Pager
						label={`${paged.pager.label}${query.hasNextPage ? "+" : ""}`}
						onPrev={paged.pager.onPrev}
						onNext={next}
						className="border-0 px-0 pt-0"
					/>
				)}
				{query.isFetchNextPageError && (
					<p role="alert" className="m-0 text-bad-fg">
						{getErrorMessage(query.error, "Could not load more deliveries")}
					</p>
				)}
			</SheetBody>
		</>
	);
}

function DeliveryList({
	deliveries,
	isLoading,
	error,
	empty,
	onRedeliver,
	redelivering,
}: {
	deliveries: WebhookDelivery[];
	isLoading: boolean;
	error: unknown;
	empty: string;
	onRedeliver: (id: string) => void;
	redelivering: boolean;
}) {
	const nowMs = useNow().getTime();
	if (isLoading) {
		return (
			<div className="flex flex-col gap-2">
				{SKELETON_ROWS.map((i) => (
					<Skeleton key={i} className="h-9 w-full" />
				))}
			</div>
		);
	}
	if (error !== null) {
		return <p className="m-0 py-6 text-center text-bad-fg">{getErrorMessage(error, "Failed to load deliveries")}</p>;
	}
	if (deliveries.length === 0) return <p className="m-0 py-6 text-center text-fg3">{empty}</p>;
	return (
		<ul aria-label="Deliveries" className="m-0 flex list-none flex-col p-0">
			{deliveries.map((d) => {
				const detail = deliveryDetail(d);
				return (
					<li
						key={d.id}
						className="grid grid-cols-[110px_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 border-b border-line py-2.5 last:border-b-0"
					>
						<span className="flex items-center gap-2 text-[12.5px]">
							<StatusGlyph glyph={deliveryTick(d)} />
							{deliveryLabel(d)}
						</span>
						<span className="truncate font-mono text-[12px]">{d.eventType}</span>
						<span
							className="text-right font-mono text-[11.5px] whitespace-nowrap text-fg3"
							title={`${formatDay(d.createdAt)} · ${formatClock(tsMs(d.createdAt))}`}
						>
							{deliveryWhen(d, nowMs)}
						</span>
						{detail !== null && (
							<span className="col-start-2 col-end-4 font-mono text-[11.5px] [overflow-wrap:anywhere] text-fg3">
								{detail}
							</span>
						)}
						{(d.status === STATUS_SUCCEEDED || d.status === STATUS_FAILED) && (
							<Button
								className="col-start-2 w-fit"
								variant="outline"
								size="sm"
								disabled={redelivering}
								onClick={() => {
									onRedeliver(d.id);
								}}
							>
								Redeliver
							</Button>
						)}
					</li>
				);
			})}
		</ul>
	);
}
