import { useTimePreferences } from "@/lib/use-time-preferences";
import { createConnectQueryKey, useMutation } from "@connectrpc/connect-query";
import type { Webhook, WebhookDelivery } from "@gen/loco/webhook/v1/webhook_pb";
import { keepPreviousData, useQueryClient } from "@tanstack/react-query";
import { toast } from "@/lib/toast";
import {
	listWebhooks,
	redeliverWebhookDelivery,
	listWebhookDeliveries,
} from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { CheckIcon, CopyIcon, RotateCwIcon } from "lucide-react";
import { CopyId } from "@/components/design/CopyId";
import { Pager } from "@/components/design/Pager";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useCopy } from "@/hooks/useCopy";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { useNow } from "@/hooks/useNow";
import { useTokenPage } from "@/hooks/useTokenPage";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { formatTotal, tokenPageLabel, tokenPager } from "@/lib/paging";
import { formatClock, tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { WebhookActions } from "./WebhookActions";
import { formatDay } from "../settings/parts";
import {
	deliveryDetail,
	deliveryLabel,
	deliveryTick,
	deliveryWhen,
	refreshDeliveries,
	STATUS_FAILED,
	STATUS_SUCCEEDED,
} from "./webhooks";
import { withCode } from "@/components/design/InlineCode";

const PAGE_SIZES = [25, 50, 100] as const;
const SKELETON_ROWS = [0, 1, 2];

// Base UI's Toggle swaps an empty value for a generated id, so All needs a real one.
const ALL = "all";
const FAILING = "failing";
const FILTERS = [
	{ value: ALL, label: "All" },
	{ value: FAILING, label: "Failing" },
] as const;
type Filter = (typeof FILTERS)[number]["value"];

function UrlCopy({ url }: { url: string }) {
	const [copied, copy] = useCopy(1500);
	return (
		<Button
			variant="ghost"
			size="icon-xs"
			title="Copy URL"
			aria-label="Copy URL"
			onClick={() => {
				copy(url, url, "Copied URL");
			}}
			className="shrink-0"
		>
			{copied === url ? <CheckIcon /> : <CopyIcon />}
		</Button>
	);
}

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
	const [size, setSize] = useState<number>(PAGE_SIZES[0]);
	// The drawer's filter lives in component state, so its page tokens do too.
	const [tokens, setTokens] = useState<string[]>([]);
	const queryClient = useQueryClient();
	const { query, nextPageToken, total, counts } = useTokenPage(
		listWebhookDeliveries,
		{ workspaceId, webhookId: webhook.id, status: "", failing: filter === FAILING, pageSize: size },
		tokens,
		{ refetchInterval: 15_000, placeholderData: keepPreviousData },
	);
	const loaded = query.data?.deliveries ?? [];
	// The shown page stays while the next one loads, so the pressed button keeps its keyboard focus.
	const pager = tokenPager(tokens, nextPageToken, (next) => {
		if (!query.isPlaceholderData) setTokens(next ?? []);
	});
	const refresh = () => {
		void refreshDeliveries(queryClient);
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

	return (
		<>
			<SheetHeader>
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="text-[12px] text-fg3">Webhook</span>
					<SheetTitle
						className={cn("truncate text-[15px] font-medium", webhook.name === "" && "font-mono")}
						title={webhook.url}
					>
						{webhook.name === "" ? new URL(webhook.url).host : webhook.name}
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
				<dl className="grid grid-cols-[84px_minmax(0,1fr)] text-[12.5px] leading-[22px] [&>*]:border-line [&>*]:py-2 [&>:not(:nth-last-child(-n+2))]:border-b">
					<dt className="text-fg3">URL</dt>
					<dd className="m-0 flex min-w-0 items-start gap-1 font-mono">
						<span className="min-w-0 break-all">{webhook.url}</span>
						<UrlCopy url={webhook.url} />
					</dd>
					<dt className="text-fg3">Events</dt>
					<dd className="m-0">
						{webhook.eventTypes.length === 0 ? (
							<span className="text-[13px]">All events in this workspace</span>
						) : (
							<ul aria-label="Events" className="m-0 flex list-none flex-wrap gap-1 p-0">
								{webhook.eventTypes.map((c) => (
									<li
										key={c}
										data-slot="chip"
										className="rounded-[5px] bg-panel2 px-[7px] font-mono text-[11.5px] leading-5 text-fg2"
									>
										{c}
									</li>
								))}
							</ul>
						)}
					</dd>
					<dt className="text-fg3">Created</dt>
					<dd className="m-0">
						{formatDay(webhook.createdAt, timePref)}, {formatClock(tsMs(webhook.createdAt), false, timePref)}
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
								Also signed with the previous secret until {formatDay(webhook.previousSecretExpiresAt, timePref)},{" "}
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
							setTokens([]);
						}}
					>
						{FILTERS.map((f) => (
							<ToggleGroupItem key={f.value} value={f.value}>
								{f.label}
								{counts !== undefined && (
									<span className="text-fg3">
										{formatTotal(f.value === ALL ? counts.allCount : counts.failingCount)}
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
					deliveries={loaded}
					isLoading={query.isPending}
					error={query.data === undefined ? query.error : null}
					onRedeliver={(deliveryId) => {
						const eventType = loaded.find((d) => d.id === deliveryId)?.eventType ?? "event";
						redeliver.mutate(
							{ workspaceId, webhookId: webhook.id, deliveryId },
							{
								onSuccess: () => {
									toast.success(withCode`Redelivering ${eventType}`);
								},
							},
						);
					}}
					redelivering={redeliver.isPending}
					empty={filter === ALL ? "No deliveries" : "No failed deliveries"}
				/>
				{query.error !== null && (
					<Button
						variant="outline"
						onClick={() => {
							if (!query.isFetching) void query.refetch();
						}}
					>
						Retry deliveries
					</Button>
				)}
				<Pager
					label={tokenPageLabel(pager.page, size, loaded.length, total)}
					page={pager.page}
					total={loaded.length}
					pageSizes={PAGE_SIZES}
					pageSize={size}
					onPageSize={(next) => {
						setSize(next);
						setTokens([]);
					}}
					onPrev={pager.onPrev}
					onNext={pager.onNext}
					className="border-0 px-0 pt-0"
				/>
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
							<StatusGlyph glyph={deliveryTick(d)} />
							{deliveryLabel(d)}
						</span>
						<span className="truncate font-mono text-[12px]">{d.eventType}</span>
						<span
							className="text-right font-mono text-[11.5px] whitespace-nowrap text-fg3"
							title={`${formatDay(d.createdAt, timePref)}, ${formatClock(tsMs(d.createdAt), false, timePref)}`}
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
