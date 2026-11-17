import { ArrowDownIcon, ArrowUpIcon, MinusIcon, PlusIcon, WebhookIcon } from "lucide-react";
import type { KeyboardEventHandler } from "react";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { CodeBlock } from "@/components/design/CodeBlock";
import { CopyId } from "@/components/design/CopyId";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { RowMenu } from "@/components/design/RowMenu";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { useCopy } from "@/hooks/useCopy";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { useNow } from "@/hooks/useNow";
import { aroundWindow } from "@/lib/obs-time";
import { auditPath, resourcePath, webhooksPath } from "@/lib/routes";
import { agoLabel, formatFullTime } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

import { useObs } from "./context";
import { eventDataLabel, eventReasonLabel, eventJson, eventValue, severityStyle, type EventItem } from "./events";
import { quoteValue, withToken } from "./query";

type Props = {
	onKeyDown: KeyboardEventHandler<HTMLDivElement>;
	item: EventItem | null;
	onPrev: (() => void) | null;
	onNext: (() => void) | null;
	onClose: () => void;
};

export function EventDetail({ item, onClose, onKeyDown, ...nav }: Props) {
	const shown = useHeldWhileClosed(item, item !== null);
	return (
		<Sheet
			open={item !== null}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<SheetContent size="wide" onKeyDown={onKeyDown}>
				{shown !== null && <EventBody item={shown} {...nav} />}
			</SheetContent>
		</Sheet>
	);
}

function EventBody({ item, onPrev, onNext }: Omit<Props, "onClose" | "item" | "onKeyDown"> & { item: EventItem }) {
	const { orgId, workspaceId, tokens, text, setQuery, goTo } = useObs();
	const { environments } = useEnvironments();
	const admin = useMyScopes().workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.ADMIN);
	const pref = useTimePreferences();
	const now = useNow();
	const [, copy] = useCopy();
	const raw = eventJson(item);
	const style = severityStyle(item.severity);
	const environment = environments.find((e) => e.id === item.environmentId)?.name ?? item.environmentId;
	const fields = [
		{ key: "type", value: item.type, filter: true },
		{ key: "resource", value: item.resourceName, filter: true },
		{ key: "environment", value: environment, filter: false },
		{ key: "region", value: item.region, filter: true },
		{ key: "reason", value: item.reason, filter: true },
		{ key: "actor", value: eventValue(item, "actor"), filter: true },
		{ key: "email", value: item.actorEmail, filter: false },
		...Object.entries(item.data)
			.filter(
				([key, value]) =>
					value !== null &&
					value !== "" &&
					![
						"region",
						"reason",
						"message",
						"resource_name",
						"creator_name",
						"creator_email",
						"environmentId",
						"environment_id",
					].includes(key),
			)
			.map(([key, value]) => ({
				key: eventDataLabel(key),
				value: typeof value === "string" ? value : JSON.stringify(value),
				filter: false,
			})),
	].filter((field) => field.value !== "");
	const filter = (key: string, value: string, neg: boolean) => {
		setQuery(withToken(tokens, { key, value: quoteValue(value), neg }), text);
	};
	return (
		<>
			<SheetHeader>
				<StatusGlyph glyph={style.glyph} className={style.fg} />
				<SheetTitle className="min-w-0 flex-1 truncate font-medium">{item.title}</SheetTitle>
				<div className="flex shrink-0 items-center gap-1">
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label="Newer event"
						disabled={onPrev === null}
						onClick={() => {
							onPrev?.();
						}}
					>
						<ArrowUpIcon />
					</Button>
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label="Older event"
						disabled={onNext === null}
						onClick={() => {
							onNext?.();
						}}
					>
						<ArrowDownIcon />
					</Button>
					<RowMenu label="Event actions">
						{item.requestId !== "" && (
							<DropdownMenuItem
								render={<Link to={`${auditPath(orgId)}?${new URLSearchParams({ q: `@request:${item.requestId}` })}`} />}
							>
								Open in audit log
							</DropdownMenuItem>
						)}
						<DropdownMenuItem
							onClick={() => {
								copy("id", item.id, "Copied event ID");
							}}
						>
							Copy event ID
						</DropdownMenuItem>
						<DropdownMenuItem
							onClick={() => {
								copy("json", raw, "Copied event as JSON");
							}}
						>
							Copy as JSON
						</DropdownMenuItem>
						<DropdownMenuSeparator />
						{(["logs", "metrics"] as const).map((view) => (
							<DropdownMenuItem
								key={view}
								onClick={() => {
									goTo(view, {
										range: aroundWindow(item.ts, Date.now()),
										tokens: [{ neg: false, key: "resource", value: item.resourceName }],
										focusTs: item.ts,
									});
								}}
							>
								{view === "logs" ? "Logs" : "Metrics"} around this event
							</DropdownMenuItem>
						))}
						<DropdownMenuItem render={<Link to={resourcePath(orgId, workspaceId, item.resourceId)} />}>
							Open resource
						</DropdownMenuItem>
					</RowMenu>
				</div>
			</SheetHeader>
			<SheetBody>
				<div className="flex flex-wrap items-center gap-2 text-[12px] text-fg3">
					<time dateTime={new Date(item.ts).toISOString()}>{formatFullTime(item.ts, false, pref)}</time>
					<span>{agoLabel(item.ts, now.getTime())}</span>
					{admin && (
						<Button
							className="ml-auto"
							variant="ghost"
							nativeButton={false}
							render={<Link to={webhooksPath(orgId, workspaceId, item.type)} />}
							title={`Get a webhook for every ${item.type}`}
						>
							<WebhookIcon />
							Subscribe
						</Button>
					)}
				</div>
				{item.message !== "" && (
					<CodeBlock className="shrink-0" copyLabel="Copy message" lineNumbers={false}>
						{item.message}
					</CodeBlock>
				)}
				<section aria-label="Attributes" className="flex min-w-0 flex-col gap-1">
					<h3 className="m-0 text-[12.5px] font-normal text-fg3">Attributes</h3>
					<dl className="m-0">
						{fields.map((field) => (
							<div
								key={field.key}
								className="group/attr grid grid-cols-[100px_minmax(0,1fr)_auto] items-center gap-2 border-b border-line py-1.5 last:border-b-0"
							>
								<dt className="truncate font-mono text-[12px] text-fg3" title={field.key}>
									{field.key}
								</dt>
								<dd className="m-0 break-words font-mono text-[12px]">
									{field.key === "reason" ? eventReasonLabel(field.value) : field.value}
								</dd>
								<span className="flex opacity-60 group-hover/attr:opacity-100 group-focus-within/attr:opacity-100">
									{field.filter && (
										<>
											<Button
												variant="ghost"
												size="icon-xs"
												title="Only this value"
												aria-label={`Only events with ${field.key} ${field.value}`}
												onClick={() => {
													filter(field.key, field.value, false);
												}}
											>
												<PlusIcon />
											</Button>
											<Button
												variant="ghost"
												size="icon-xs"
												title="Hide this value"
												aria-label={`Hide events with ${field.key} ${field.value}`}
												onClick={() => {
													filter(field.key, field.value, true);
												}}
											>
												<MinusIcon />
											</Button>
										</>
									)}
								</span>
							</div>
						))}
						{item.requestId !== "" && (
							<div className="grid grid-cols-[100px_minmax(0,1fr)] items-center gap-2 py-1.5">
								<dt className="font-mono text-[12px] text-fg3">request</dt>
								<dd className="m-0 min-w-0">
									<CopyId
										value={item.requestId}
										title="Copy request ID"
										className="max-w-full bg-panel2 font-mono text-[12px]"
									>
										<span className="truncate">{item.requestId}</span>
									</CopyId>
								</dd>
							</div>
						)}
					</dl>
				</section>
				<section aria-label="Raw event" className="flex min-w-0 flex-col gap-1">
					<h3 className="m-0 text-[12.5px] font-normal text-fg3">Raw event</h3>
					<CodeBlock language="json" className="max-h-[min(60vh,640px)] shrink-0" copyLabel="Copy raw event">
						{raw}
					</CodeBlock>
				</section>
			</SheetBody>
		</>
	);
}
