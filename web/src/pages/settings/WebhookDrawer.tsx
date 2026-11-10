import { useQuery } from "@connectrpc/connect-query";
import type { Webhook, WebhookDelivery } from "@gen/loco/webhook/v1/webhook_pb";
import { listWebhookDeliveries } from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useState } from "react";

import { Badge } from "@/components/design/Badge";
import { Pager } from "@/components/design/Pager";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage } from "@/lib/error-handler";
import { usePagedRows } from "@/lib/paging";
import { formatClock, tsMs } from "@/lib/time";

import { formatDay } from "./parts";
import {
	deliveryDetail,
	deliveryLabel,
	deliveryTick,
	deliveryWhen,
	eventsSummary,
	failingCount,
	isFailing,
} from "./webhooks";

const PAGE_SIZES = [10] as const;
const SKELETON_ROWS = [0, 1, 2];

type Filter = "all" | "failing";

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
	const query = useQuery(listWebhookDeliveries, { workspaceId, webhookId: webhook.id });
	const [filter, setFilter] = useState<Filter>("all");
	const all = query.data?.deliveries ?? [];
	const failing = failingCount(all);
	const shown = filter === "all" ? all : all.filter(isFailing);
	const paged = usePagedRows(shown, PAGE_SIZES);
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
				<ul aria-label="Events" className="m-0 flex list-none flex-wrap gap-1.5 p-0">
					{chips.map((c) => (
						<li key={c}>
							<Badge tone="muted" size="sm" className="font-mono font-normal">
								{c}
							</Badge>
						</li>
					))}
				</ul>
				<div className="flex items-center justify-between gap-2.5">
					<h3 className="m-0 text-[14px] font-medium">Deliveries</h3>
					<ToggleGroup
						variant="segmented"
						aria-label="Show deliveries"
						value={[filter]}
						onValueChange={(v: string[]) => {
							if (v[0] === "all" || v[0] === "failing") {
								setFilter(v[0]);
								paged.reset();
							}
						}}
					>
						<ToggleGroupItem value="all" count={all.length}>
							All
						</ToggleGroupItem>
						<ToggleGroupItem value="failing" count={failing}>
							Failing
						</ToggleGroupItem>
					</ToggleGroup>
				</div>
				<DeliveryList
					deliveries={paged.rows}
					isLoading={query.isPending}
					error={query.error}
					empty={filter === "all" ? "No deliveries yet. Events appear here as they are sent." : "Nothing failing."}
				/>
				{shown.length > 0 && <Pager {...paged.pager} className="border-0 px-0 pt-0" />}
			</SheetBody>
		</>
	);
}

function DeliveryList({
	deliveries,
	isLoading,
	error,
	empty,
}: {
	deliveries: WebhookDelivery[];
	isLoading: boolean;
	error: unknown;
	empty: string;
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
					</li>
				);
			})}
		</ul>
	);
}
