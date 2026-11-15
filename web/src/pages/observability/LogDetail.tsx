import { useState } from "react";
import { Link } from "react-router";
import {
	BoxIcon,
	ChartLineIcon,
	CheckIcon,
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
import { CodeBlock } from "@/components/design/CodeBlock";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { SearchInput } from "@/components/design/SearchInput";
import { useCopy } from "@/hooks/useCopy";
import { AROUND_MS, aroundWindow, MINUTE_MS } from "@/lib/obs-time";
import { resourcePath } from "@/lib/routes";
import { cn } from "@/lib/utils";

import type { ColumnKey } from "./columns";
import { useObs } from "./context";
import { bodyParts, flattenJson, fmtTs, levelStyle } from "./format";
import { reservedLabelReason, REQUEST_FILTER_ATTRIBUTES, sameToken, type FieldKey, type Token } from "./query";
import type { LogRow } from "./rows";
import { CopyButton, DetailPanel, Dot, PanelNav } from "./shared";

const NUMERIC_RE = /^\d+(\.\d+)?$/;
const AROUND_LABEL = `±${String(AROUND_MS / MINUTE_MS)} minutes`;

interface Attribute {
	key: string;
	value: string;
	/** The token filtering on this value, or why the value can't be filtered. */
	filter: { key: FieldKey; value: string } | string;
	column?: ColumnKey | undefined;
}

const REQUEST_FIELD_BY_ATTRIBUTE = new Map(
	Object.entries(REQUEST_FILTER_ATTRIBUTES).map(([field, attribute]) => [attribute, field as FieldKey]),
);

function attributes(row: LogRow): Attribute[] {
	const e = row.entry;
	const list: Attribute[] = [
		{ key: "timestamp", value: new Date(row.ts).toISOString(), filter: "Use the time range to pick a time" },
		{ key: "level", value: row.level, filter: { key: "level", value: row.level }, column: "level" },
		{
			key: "resource",
			value: row.resourceName,
			filter: { key: "resource", value: row.resourceName },
			column: "resource",
		},
		{ key: "replica", value: row.pod, filter: { key: "replica", value: row.pod }, column: "replica" },
		{ key: "region", value: row.region, filter: { key: "region", value: row.region }, column: "region" },
		{ key: "trace_id", value: e.traceId, filter: { key: "trace", value: e.traceId }, column: "trace" },
		{ key: "span_id", value: e.spanId, filter: "Filter by trace_id to see the request" },
	];
	const extra = new Map<string, Attribute>();
	for (const [key, value] of Object.entries(e.resourceAttributes)) {
		if (key === "k8s.pod.name" || key === "loco.region") continue;
		const filter = reservedLabelReason(key) ?? { key: "label" as const, value: `${key}=${value}` };
		extra.set(key, { key, value, filter, column: key });
	}
	for (const [key, value] of Object.entries(e.logAttributes)) {
		const field = REQUEST_FIELD_BY_ATTRIBUTE.get(key);
		const filter = field === undefined ? { key: "attr" as const, value: `${key}=${value}` } : { key: field, value };
		extra.set(key, { key, value, filter, column: key });
	}
	for (const [key, value] of Object.entries(row.json !== null ? flattenJson(row.json) : {})) {
		extra.set(key, { key, value, filter: "Fields of a JSON message can't be filtered; search its text", column: key });
	}
	return [...list, ...extra.values()].filter((a) => a.value !== "");
}

export function LogDetail({
	row,
	words,
	columns,
	onColumns,
	onPrev,
	onNext,
	onClose,
}: {
	row: LogRow;
	words: string[];
	columns: ColumnKey[];
	onColumns: (next: ColumnKey[]) => void;
	onPrev: (() => void) | null;
	onNext: (() => void) | null;
	onClose: () => void;
}) {
	const { tokens, text, setQuery, goTo, orgId, workspaceId, resourceById } = useObs();
	const [attrQ, setAttrQ] = useState("");
	const [copied, copy] = useCopy();
	const style = levelStyle(row.level);
	const e = row.entry;
	const known = resourceById.get(e.resourceId);

	const aq = attrQ.toLowerCase();
	const attrs = attributes(row).filter(
		(a) => aq === "" || a.key.toLowerCase().includes(aq) || a.value.toLowerCase().includes(aq),
	);
	const addTok = (t: Token) => {
		if (tokens.some((x) => sameToken(x, t))) return;
		setQuery([...tokens.filter((x) => !(x.key === t.key && x.value === t.value)), t], text);
	};
	const json = JSON.stringify(
		{
			timestamp: new Date(row.ts).toISOString(),
			level: row.label,
			message: row.json ?? e.body,
			resource: row.resourceName,
			replica: row.pod,
			region: row.region,
			trace_id: e.traceId,
			span_id: e.spanId,
			resource_attributes: e.resourceAttributes,
			log_attributes: e.logAttributes,
		},
		null,
		2,
	);
	const msgText = row.json !== null ? JSON.stringify(row.json, null, 2) : e.body;

	return (
		<DetailPanel
			label="Log line"
			header={
				<>
					<div className="flex min-w-0 flex-1 flex-col gap-1">
						<span className="flex min-w-0 items-center gap-2">
							<Dot size={8} color={row.color} />
							<span className="truncate text-[16px] tracking-[-0.01em]">{row.resourceName}</span>
							<span className={cn("inline-flex h-5 items-center rounded-[5px] px-[7px] text-[12px]", style.badge)}>
								{row.label}
							</span>
						</span>
						<span className="truncate font-mono text-[11.5px] text-fg3">
							{fmtTs(row.ts, true)}
							{row.replica !== "" && ` · ${row.replica}`}
							{row.region !== "" && ` · ${row.region}`}
						</span>
					</div>
					<DropdownMenu>
						<DropdownMenuTrigger
							render={<Button variant="ghost" size="icon-sm" aria-label="More for this line" title="More" />}
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
									<ItemText label={`View ${known.name}`} sub="Resource page" />
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
									goTo("logs", { range: aroundWindow(row.ts, Date.now()), focusTs: row.ts });
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
				</>
			}
		>
			<div className="flex flex-1 flex-col gap-5 overflow-y-auto px-4 pt-4 pb-5">
				<div className="relative rounded-lg bg-panel shadow-[inset_0_0_0_1px_var(--line)]">
					<Button
						variant="ghost"
						size="icon-xs"
						aria-label="Copy message"
						title="Copy message"
						onClick={() => {
							copy("msg", msgText, "Copied message");
						}}
						className={cn("absolute top-2 right-2", copied === "msg" ? "text-ok-fg" : "text-fg3")}
					>
						{copied === "msg" ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
					</Button>
					<div
						className={cn(
							"max-h-[280px] overflow-auto py-3 pr-11 pl-3.5 font-mono text-[12.5px] leading-[1.55] break-words whitespace-pre-wrap",
							row.json !== null ? "text-foreground" : style.body,
						)}
					>
						{row.json !== null ? msgText : bodyParts(e.body, words)}
					</div>
				</div>
				<div className="flex flex-col gap-2">
					<div className="flex items-center gap-2">
						<span className="text-[12.5px] text-fg3">Attributes</span>
						<span className="flex-1" />
						<SearchInput
							value={attrQ}
							onChange={(ev) => {
								setAttrQ(ev.target.value);
							}}
							aria-label="Filter attributes"
							placeholder="Filter attributes"
							className="w-[180px]"
						/>
					</div>
					<div className="flex flex-col">
						{attrs.length === 0 && <div className="py-2 text-sm text-fg3">No attributes match.</div>}
						{attrs.map((a) => (
							<AttributeRow
								key={a.key}
								attribute={a}
								copied={copied === `a:${a.key}`}
								isColumn={a.column !== undefined && columns.includes(a.column)}
								onCopy={() => {
									copy(`a:${a.key}`, a.value, `Copied ${a.key}`);
								}}
								onFilter={(neg) => {
									if (typeof a.filter !== "string") addTok({ neg, ...a.filter });
								}}
								onColumn={() => {
									if (a.column !== undefined && !columns.includes(a.column)) onColumns([...columns, a.column]);
								}}
							/>
						))}
					</div>
				</div>
				<div className="flex flex-col gap-2">
					<span className="text-[12.5px] text-fg3">JSON</span>
					<CodeBlock language="json">{json}</CodeBlock>
				</div>
			</div>
		</DetailPanel>
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
	onColumn: () => void;
}) {
	const { key, value, filter } = attribute;
	const reason = typeof filter === "string" ? filter : null;
	return (
		<div className="group/attr grid grid-cols-[110px_minmax(0,1fr)_auto] items-center gap-2.5 border-b border-line py-1.5 last:border-b-0">
			<span className="truncate font-mono text-[12px] text-fg3" title={key}>
				{key}
			</span>
			<span
				className={cn(
					"font-mono text-[12px] [overflow-wrap:anywhere]",
					NUMERIC_RE.test(value) ? "text-info-fg" : "text-foreground",
				)}
			>
				{value}
			</span>
			<span className="flex gap-0.5 opacity-60 group-focus-within/attr:opacity-100 group-hover/attr:opacity-100">
				<AttrAction
					label={reason ?? `Only ${key} ${value}`}
					disabled={reason !== null}
					onClick={() => {
						onFilter(false);
					}}
				>
					<PlusIcon />
				</AttrAction>
				<AttrAction
					label={reason ?? `Hide ${key} ${value}`}
					disabled={reason !== null}
					onClick={() => {
						onFilter(true);
					}}
				>
					<MinusIcon />
				</AttrAction>
				<AttrAction
					label={isColumn ? "Already a column" : `Add ${key} as a column`}
					disabled={attribute.column === undefined || isColumn}
					onClick={onColumn}
				>
					<Columns3Icon />
				</AttrAction>
				<CopyButton copied={copied} onCopy={onCopy} title={`Copy ${key}`} className="size-6" />
			</span>
		</div>
	);
}

function AttrAction({
	label,
	disabled,
	onClick,
	children,
}: {
	label: string;
	disabled: boolean;
	onClick: () => void;
	children: React.ReactNode;
}) {
	return (
		<Button
			variant="ghost"
			size="icon-xs"
			aria-label={label}
			title={label}
			disabled={disabled}
			onClick={onClick}
			className="text-fg2 hover:text-foreground [&_svg]:size-3.5"
		>
			{children}
		</Button>
	);
}
