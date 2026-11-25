import { useTimePreferences } from "@/lib/use-time-preferences";
import { withCode } from "@/components/design/InlineCode";
import { useState, type ElementType, type ReactNode } from "react";
import { Link } from "react-router";
import {
	BoxIcon,
	ChartLineIcon,
	ClockIcon,
	Columns3Icon,
	CopyIcon,
	EllipsisIcon,
	FilterIcon,
	MinusIcon,
	PlusIcon,
	ScrollTextIcon,
	WaypointsIcon,
} from "lucide-react";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useCopy } from "@/hooks/useCopy";
import { AROUND_MS, aroundWindow, MINUTE_MS } from "@/lib/obs-time";
import { resourcePath } from "@/lib/routes";
import { useStoredValue } from "@/lib/storage";
import { formatFullTime } from "@/lib/time";
import { cn } from "@/lib/utils";

import type { ColumnKey } from "./columns";
import { useObs } from "./context";
import { bodyParts, LEVEL_LABEL, levelStyle } from "./format";
import { queryKeyReason, quoteValue, withToken, type Token } from "./query";
import { logJson, logMessageText, WORKLOAD_TITLE, type LogRow } from "./rows";
import { CopyButton, PanelNav, PlainCode } from "./shared";

const NUMERIC_RE = /^\d+(\.\d+)?$/;
const AROUND_LABEL = `±${String(AROUND_MS / MINUTE_MS)} minutes`;

interface Attribute {
	key: string;
	value: string;
	/** The query term matching this value, or why the value can't be filtered. */
	filter: Omit<Token, "neg"> | string;
	column?: ColumnKey | undefined;
}

function attributes(row: LogRow): Attribute[] {
	const e = row.entry;
	const field = (key: string, value: string) => ({ key, value: quoteValue(value) });
	const list: Attribute[] = [
		...(row.workload
			? []
			: [
					{ key: "resource", value: row.resourceName, filter: field("resource", row.resourceName), column: "resource" },
				]),
		{ key: "replica", value: row.replica, filter: field("replica", row.pod), column: "replica" },
		{ key: "region", value: row.region, filter: field("region", row.region), column: "region" },
		{ key: "level", value: row.level, filter: field("level", row.level), column: "level" },
	];
	const extra = new Map<string, Attribute>();
	for (const [key, value] of Object.entries({ ...e.resourceAttributes, ...e.logAttributes })) {
		if (key === "k8s.pod.name" || key === "loco.region") continue;
		extra.set(key, { key, value, filter: queryKeyReason(key, "logs") ?? field(key, value), column: key });
	}
	const trace: Attribute = { key: "trace", value: e.traceId, filter: field("trace", e.traceId), column: "trace" };
	const template: Attribute[] =
		row.message === e.body ? [] : [{ key: "template", value: e.body, filter: "Search the message text instead" }];
	return [...list, ...extra.values(), ...template, trace].filter((a) => a.value !== "");
}

export function LogDetail({
	row,
	title: Title,
	words,
	columns,
	onColumns,
	onPrev,
	onNext,
	onClose,
	onFilterToken,
	onAround,
}: {
	row: LogRow;
	title: ElementType<{ className?: string; children: ReactNode }>;
	words: string[];
	columns: ColumnKey[];
	onColumns?: (next: ColumnKey[]) => void;
	onPrev: (() => void) | null;
	onNext: (() => void) | null;
	onClose: () => void;
	onFilterToken?: (token: Token) => void;
	onAround?: () => void;
}) {
	const timePref = useTimePreferences();
	const { tokens, text, setQuery, goTo, orgId, workspaceId, resourceById } = useObs();
	const [attrQ, setAttrQ] = useState("");
	const [view, setView] = useStoredValue("loco:log-drawer-view", "attributes");
	const [wrapJson, setWrapJson] = useStoredValue("loco:log-drawer-json-wrap", "on");
	const showJson = view === "json";
	const [copied, copy] = useCopy();
	const style = levelStyle(row.level);
	const e = row.entry;
	const known = resourceById.get(e.resourceId);

	const aq = attrQ.toLowerCase();
	const attrs = attributes(row).filter(
		(a) => aq === "" || a.key.toLowerCase().includes(aq) || a.value.toLowerCase().includes(aq),
	);
	const addTok = (t: Token) => {
		if (onFilterToken !== undefined) {
			onFilterToken(t);
			return;
		}
		setQuery(withToken(tokens, t), text);
	};
	const json = logJson(row, timePref);
	const msgText = logMessageText(row);

	return (
		<>
			<div className="flex shrink-0 items-start gap-3 border-b border-line pt-[18px] pr-4 pb-4 pl-6">
				<div className="flex min-w-0 flex-1 flex-col gap-1.5">
					<div className="flex min-w-0 items-baseline gap-2.5">
						<Title
							className={cn(
								"m-0 truncate text-[20px] font-normal tracking-[-0.02em]",
								row.workload && "max-w-full shrink-0",
							)}
						>
							{row.resourceName}
						</Title>
						{row.workload && (
							<span className="min-w-0 truncate text-[12px] text-fg3" title={WORKLOAD_TITLE}>
								Kubernetes workload
							</span>
						)}
						<span className="truncate font-mono text-[12px] text-fg3">
							{[row.replica, row.region].filter((part) => part !== "").join(", ")}
						</span>
					</div>
					<div className="flex min-w-0 items-center gap-2">
						<span
							data-level-badge={row.level}
							className={cn("inline-flex h-5 shrink-0 items-center rounded-[5px] px-[7px] text-[12px]", style.badge)}
						>
							{LEVEL_LABEL[row.level]}
						</span>
						<span className="truncate font-mono text-[12px] text-fg3">{formatFullTime(row.ts, true, timePref)}</span>
					</div>
				</div>
				<div className="flex shrink-0 items-center gap-0.5">
					<DropdownMenu>
						<DropdownMenuTrigger
							render={<Button variant="ghost" size="icon" aria-label="More for this line" title="More" />}
						>
							<EllipsisIcon className="size-4" />
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="w-[250px]">
							{known !== undefined && (
								<DropdownMenuItem
									render={<Link to={resourcePath(orgId, workspaceId, known.id)} />}
									className="items-start"
								>
									<BoxIcon />
									<ItemText label={`View ${known.slug}`} sub="Resource page" />
								</DropdownMenuItem>
							)}
							{e.traceId !== "" && (
								<DropdownMenuItem
									className="items-start"
									onClick={() => {
										goTo("traces", {
											range: aroundWindow(row.ts, Date.now()),
											tokens: [],
											trace: e.traceId,
											...(e.spanId !== "" ? { span: e.spanId } : {}),
										});
									}}
								>
									<WaypointsIcon />
									<ItemText label="View trace" sub="Waterfall for this request" />
								</DropdownMenuItem>
							)}
							<DropdownMenuItem
								className="items-start"
								onClick={() => {
									if (onAround !== undefined) onAround();
									else goTo("logs", { range: aroundWindow(row.ts, Date.now()), focusTs: row.ts });
								}}
							>
								<ClockIcon />
								<ItemText label="Logs around this line" sub={`${AROUND_LABEL}, same filters`} />
							</DropdownMenuItem>
							{row.pod !== "" && (
								<DropdownMenuItem
									className="items-start"
									onClick={() => {
										addTok({ neg: false, key: "replica", value: row.pod });
									}}
								>
									<FilterIcon />
									<ItemText label={`Only replica ${row.replica}`} sub="Add @replica filter" />
								</DropdownMenuItem>
							)}
							{!row.workload && (
								<DropdownMenuItem
									className="items-start"
									onClick={() => {
										goTo("metrics", {
											range: aroundWindow(row.ts, Date.now()),
											tokens: [{ neg: false, key: "resource", value: row.resourceName }],
											focusTs: row.ts,
										});
									}}
								>
									<ChartLineIcon />
									<ItemText label="Metrics around this line" sub={`${row.resourceName}, ${AROUND_LABEL}`} />
								</DropdownMenuItem>
							)}
							{e.traceId !== "" && (
								<DropdownMenuItem
									className="items-start"
									onClick={() => {
										goTo("logs", {
											range: aroundWindow(row.ts, Date.now()),
											tokens: [{ neg: false, key: "trace", value: e.traceId }],
											focusTs: row.ts,
										});
									}}
								>
									<ScrollTextIcon />
									<ItemText label="Logs in this trace" sub="Every line of this request" />
								</DropdownMenuItem>
							)}
							<DropdownMenuSeparator />
							<DropdownMenuItem
								onClick={() => {
									copy("msg", msgText, "Copied message");
								}}
							>
								<CopyIcon />
								Copy message
							</DropdownMenuItem>
							<DropdownMenuItem
								onClick={() => {
									copy("json", json, "Copied as JSON");
								}}
							>
								<CopyIcon />
								Copy as JSON
							</DropdownMenuItem>
						</DropdownMenuContent>
					</DropdownMenu>
					<PanelNav onPrev={onPrev} onNext={onNext} onClose={onClose} />
				</div>
			</div>
			<div className="flex min-h-0 flex-1 flex-col gap-[18px] overflow-y-auto px-6 pt-5 pb-6">
				<section aria-label="Message" className="flex flex-col gap-2">
					<h3 className="m-0 text-[12.5px] font-normal text-fg3">Message</h3>
					<PlainCode
						copied={copied === "msg"}
						copyLabel="Copy message"
						onCopy={() => {
							copy("msg", msgText, "Copied message");
						}}
						className={row.json !== null ? undefined : style.body}
					>
						{row.json !== null ? msgText : bodyParts(row.message, words)}
					</PlainCode>
				</section>
				<div className="flex flex-col gap-2">
					<div className="flex items-center gap-2">
						<ToggleGroup
							variant="segmented"
							aria-label="Detail view"
							value={[view]}
							onValueChange={(v: string[]) => {
								if (v[0] !== undefined) setView(v[0]);
							}}
						>
							<ToggleGroupItem value="attributes">Attributes</ToggleGroupItem>
							<ToggleGroupItem value="json">JSON</ToggleGroupItem>
						</ToggleGroup>
						<span className="flex-1" />
						{!showJson && (
							<SearchInput
								value={attrQ}
								onChange={(ev) => {
									setAttrQ(ev.target.value);
								}}
								aria-label="Filter attributes"
								placeholder="Filter attributes"
								className="w-[180px]"
							/>
						)}
					</div>
					{showJson ? (
						<PlainCode
							copied={copied === "json"}
							copyLabel="Copy JSON"
							onCopy={() => {
								copy("json", json, "Copied as JSON");
							}}
							wrap={wrapJson === "on"}
							onWrap={(wrap) => {
								setWrapJson(wrap ? "on" : "off");
							}}
						>
							{json}
						</PlainCode>
					) : (
						<div className="grid grid-cols-[fit-content(45%)_minmax(0,1fr)_auto]">
							{attrs.length === 0 && <div className="py-2 text-sm text-fg3">No attributes match.</div>}
							{attrs.map((a) => (
								<AttributeRow
									key={a.key}
									attribute={a}
									copied={copied === `a:${a.key}`}
									isColumn={a.column !== undefined && columns.includes(a.column)}
									onCopy={() => {
										copy(`a:${a.key}`, a.value, withCode`Copied ${a.key}`);
									}}
									onFilter={(neg) => {
										if (typeof a.filter !== "string") addTok({ neg, ...a.filter });
									}}
									onColumn={
										onColumns === undefined
											? undefined
											: () => {
													if (a.column !== undefined && !columns.includes(a.column)) onColumns([...columns, a.column]);
												}
									}
								/>
							))}
						</div>
					)}
				</div>
			</div>
		</>
	);
}

function ItemText({ label, sub }: { label: string; sub: string }) {
	return (
		<span className="flex min-w-0 flex-col">
			<span className="truncate">{label}</span>
			<span className="truncate text-[12px] text-fg3">{sub}</span>
		</span>
	);
}

function AttributeRow({
	attribute,
	copied,
	isColumn,
	onCopy,
	onFilter,
	onColumn,
}: {
	attribute: Attribute;
	copied: boolean;
	isColumn: boolean;
	onCopy: () => void;
	onFilter: (neg: boolean) => void;
	onColumn: (() => void) | undefined;
}) {
	const { key, value, filter } = attribute;
	const reason = typeof filter === "string" ? filter : null;
	return (
		<div className="group/attr grid grid-cols-subgrid col-span-full items-start gap-2.5 border-b border-line py-1.5 last:border-b-0">
			<span className="font-mono text-[12.5px] [overflow-wrap:anywhere] text-fg3" title={key}>
				{reason === null ? `@${key}` : key}
			</span>
			<span
				className={cn(
					"font-mono text-[12px] [overflow-wrap:anywhere]",
					NUMERIC_RE.test(value) ? "text-info-fg" : "text-foreground",
				)}
			>
				{value}
			</span>
			<span className="flex gap-px self-center opacity-40 group-focus-within/attr:opacity-100 group-hover/attr:opacity-100">
				<AttrAction
					label={reason ?? `Only ${key} ${value}`}
					tip={reason ?? "Only this value"}
					disabled={reason !== null}
					onClick={() => {
						onFilter(false);
					}}
				>
					<PlusIcon />
				</AttrAction>
				<AttrAction
					label={reason ?? `Hide ${key} ${value}`}
					tip={reason ?? "Hide this value"}
					disabled={reason !== null}
					onClick={() => {
						onFilter(true);
					}}
				>
					<MinusIcon />
				</AttrAction>
				{onColumn !== undefined && (
					<AttrAction
						label={isColumn ? "Already a column" : `Add ${key} as a column`}
						tip={isColumn ? "Already a column" : "Add as a column"}
						disabled={attribute.column === undefined || isColumn}
						onClick={onColumn}
					>
						<Columns3Icon />
					</AttrAction>
				)}
				<CopyButton copied={copied} onCopy={onCopy} title={`Copy ${key}`} className="size-5" />
			</span>
		</div>
	);
}

function AttrAction({
	label,
	tip,
	disabled,
	onClick,
	children,
}: {
	label: string;
	tip: string;
	disabled: boolean;
	onClick: () => void;
	children: React.ReactNode;
}) {
	return (
		<Button
			variant="ghost"
			size="icon-xs"
			aria-label={label}
			title={tip}
			disabled={disabled}
			onClick={onClick}
			className="size-5 text-fg2 hover:text-foreground [&_svg]:size-3"
		>
			{children}
		</Button>
	);
}
