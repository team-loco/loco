import { useTimePreferences } from "@/lib/use-time-preferences";
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
import { RotateCwIcon } from "lucide-react";
import { CopyId } from "@/components/design/CopyId";
import { Badge } from "@/components/design/Badge";
import { Pager } from "@/components/design/Pager";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { nextPage, pagedRows, type Paging } from "@/lib/paging";
import { formatClock, tsMs } from "@/lib/time";

import { WebhookActions } from "./WebhookActions";
import { formatDay } from "../settings/parts";
import {
	deliveryDetail,
	deliveryLabel,
	deliveryGlyph,
	deliveryWhen,
	STATUS_FAILED,
	STATUS_SUCCEEDED,
} from "./webhooks";

const PAGE_SIZES = [25, 50, 100] as const;
const FIRST_PAGE: Paging = { page: 0, size: PAGE_SIZES[0] };
const SKELETON_ROWS = [0, 1, 2];

// Base UI's Toggle swaps an empty value for a generated id, so All needs a real one.
const ALL = "all";
const FAILING = "failing";
const FILTERS = [
	{ value: ALL, label: "All" },
	{ value: FAILING, label: "Failing" },
] as const;
type Filter = (typeof FILTERS)[number]["value"];

export function WebhookDrawer({
	workspaceId,
	workspaceName,
	webhook,
	onClose,
	onDelete,
}: {
	onDelete: () => void;
	workspaceId: string;
	workspaceName: string;
	webhook: Webhook | null;
	onClose: () => void;
}) {
	const shown = useHeldWhileClosed(webhook, webhook !== null);
	return (
		<Sheet
			open={webhook !== null}
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
		>
			<SheetContent size="wide" className="data-[side=right]:w-[min(560px,calc(100vw-16px))]">
				{shown !== null && (
					<WebhookDetail
						onDelete={onDelete}
						key={shown.id}
						workspaceId={workspaceId}
						workspaceName={workspaceName}
						webhook={shown}
					/>
				)}
			</SheetContent>
		</Sheet>
	);
}

function WebhookDetail({
	workspaceId,
	workspaceName,
	webhook,
	onDelete,
}: {
	onDelete: () => void;
	workspaceId: string;
	workspaceName: string;
	webhook: Webhook;
}) {
	const timePref = useTimePreferences();
	const [filter, setFilter] = useState<Filter>(ALL);
	const [paging, setPaging] = useState(FIRST_PAGE);
	const queryClient = useQueryClient();
	const query = useInfiniteQuery(
		listWebhookDeliveries,
		{
			workspaceId,
			webhookId: webhook.id,
			status: "",
			failing: filter === FAILING,
			pageSize: paging.size,
			pageToken: "",
		},
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
		query.isFetchingNextPage,
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
			refresh();
		},
		onError: (error) => {
			toastConnectError(error, "Could not redeliver the event");
		},
	});
	const chips = webhook.eventTypes.length === 0 ? ["All events in this workspace"] : webhook.eventTypes;

	return (
		<>
			<SheetHeader>
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="text-[12px] text-fg3">Webhook</span>
					<SheetTitle className="truncate font-mono text-[15px] font-medium" title={webhook.url}>
						{new URL(webhook.url).host}
					</SheetTitle>
				</div>
				<WebhookActions
					workspaceId={workspaceId}
					workspaceName={workspaceName}
					webhook={webhook}
					onChanged={refresh}
					onDelete={onDelete}
				/>
			</SheetHeader>
			<SheetBody>
				<dl className="grid grid-cols-[84px_minmax(0,1fr)] items-start gap-y-3 text-[12.5px]">
					<dt className="text-fg3">URL</dt>
					<dd className="m-0 flex min-w-0 items-start gap-1 font-mono">
						<CopyId
							value={webhook.url}
							title="Copy URL"
							toastText="Copied URL"
							className="h-auto max-w-full justify-start border-0 px-0 py-0 font-mono text-[12.5px] font-normal whitespace-normal"
						>
							<span className="break-all text-left">{webhook.url}</span>
						</CopyId>
					</dd>
					<dt className="text-fg3">Events</dt>
					<dd className="m-0">
						<ul aria-label="Events" className="m-0 flex list-none flex-wrap gap-1.5 p-0">
							{chips.map((c) => (
								<li key={c}>
									<Badge tone="muted" size="sm" className="font-mono font-normal">
										{c}
									</Badge>
								</li>
							))}
						</ul>
					</dd>
					<dt className="text-fg3">Created</dt>
					<dd className="m-0">
						{formatDay(webhook.createdAt, timePref)} · {formatClock(tsMs(webhook.createdAt), false, timePref)}
					</dd>
					<dt className="text-fg3">ID</dt>
					<dd className="m-0">
						<CopyId
							value={webhook.id}
							title="Copy webhook ID"
							toastText="Copied webhook ID"
							className="h-auto max-w-full justify-start border-0 px-0 py-0 font-mono text-[12.5px] font-normal whitespace-normal"
						>
							<span className="break-all text-left">{webhook.id}</span>
						</CopyId>
					</dd>
					{webhook.previousSecretExpiresAt !== undefined && (
						<>
							<dt className="text-fg3">Signing</dt>
							<dd className="m-0">
								Also signed with the previous secret until {formatDay(webhook.previousSecretExpiresAt, timePref)} ·{" "}
								{formatClock(tsMs(webhook.previousSecretExpiresAt), false, timePref)}
							</dd>
						</>
					)}
				</dl>
				<div className="flex flex-wrap items-center justify-between gap-2.5">
					<h3 className="m-0 text-[14px] font-medium">Deliveries</h3>
					<ToggleGroup
						variant="segmented"
						aria-label="Show deliveries"
						className="max-w-full flex-wrap"
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
								{query.data !== undefined && (
									<span className="text-fg3">
										{(f.value === ALL
											? query.data.pages[0]?.totalCount
											: query.data.pages[0]?.failingCount
										)?.toString()}
									</span>
								)}
							</ToggleGroupItem>
						))}
					</ToggleGroup>
				</div>
				{query.data !== undefined && query.isRefetchError && (
					<p role="alert" className="m-0 text-bad-fg">
						{getErrorMessage(query.error, "Could not refresh deliveries")}
					</p>
				)}
				<DeliveryList
					deliveries={paged.rows}
					isLoading={query.isPending}
					error={query.data === undefined ? query.error : null}
					onRedeliver={(deliveryId) => {
						const eventType = loaded.find((d) => d.id === deliveryId)?.eventType ?? "event";
						redeliver.mutate(
							{ workspaceId, webhookId: webhook.id, deliveryId },
							{
								onSuccess: () => {
									toast.success(`Redelivering ${eventType}`);
								},
							},
						);
					}}
					redelivering={redeliver.isPending}
					empty={filter === ALL ? "No deliveries yet. Events appear here as they are sent." : "Nothing failing."}
				/>
				{query.error !== null && (
					<Button
						variant="outline"
						onClick={() => {
							if (query.isFetching) return;
							if (query.isFetchNextPageError) next?.();
							else void query.refetch();
						}}
					>
						Retry deliveries
					</Button>
				)}
				<Pager
					{...paged.pager}
					label={`${paged.pager.label}${query.hasNextPage ? "+" : ""}`}
					onNext={next}
					className="border-0 px-0 pt-0"
				/>
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
	const timePref = useTimePreferences();
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
						className="grid grid-cols-[90px_minmax(0,1fr)_70px_28px] sm:grid-cols-[110px_minmax(0,1fr)_100px_28px] items-center gap-x-3 gap-y-1 border-b border-line py-2.5 last:border-b-0"
					>
						<span className="flex items-center gap-2 text-[12.5px]">
							<StatusGlyph glyph={deliveryGlyph(d)} />
							{deliveryLabel(d)}
						</span>
						<span className="truncate font-mono text-[12px]">{d.eventType}</span>
						<span
							className="text-right font-mono text-[11.5px] whitespace-nowrap text-fg3"
							title={`${formatDay(d.createdAt, timePref)} · ${formatClock(tsMs(d.createdAt), false, timePref)}`}
						>
							{deliveryWhen(d, nowMs)}
						</span>
						<span className="col-start-4 row-start-1">
							{(d.status === STATUS_SUCCEEDED || d.status === STATUS_FAILED) && (
								<Button
									variant="ghost"
									size="icon-sm"
									title="Redeliver"
									aria-label={`Redeliver ${d.eventType}`}
									aria-disabled={redelivering}
									onClick={() => {
										if (!redelivering) onRedeliver(d.id);
									}}
								>
									<RotateCwIcon />
								</Button>
							)}
						</span>
						{detail !== null && (
							<span className="col-start-2 col-end-5 font-mono text-[11.5px] break-words text-fg3">{detail}</span>
						)}
					</li>
				);
			})}
		</ul>
	);
}
