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
import { SoonTag } from "@/components/design/SoonTag";
import { useCopy } from "@/hooks/useCopy";
import { aroundWindow } from "@/lib/obs-time";
import { resourcePath } from "@/lib/routes";
import { cn } from "@/lib/utils";

import { rowAttributes, type ColumnKey } from "./columns";
import { useObs } from "./context";
import { bodyParts, fmtTs, levelStyle } from "./format";
import { sameToken, type FieldKey, type Token } from "./query";
import type { LogRow } from "./rows";
import { CopyButton, DetailPanel, Dot, PanelNav } from "./shared";

const NUMERIC_RE = /^\d+(\.\d+)?$/;

interface Attribute {
	key: string;
	value: string;
	field?: FieldKey | undefined;
	column?: ColumnKey | undefined;
}

function attributes(row: LogRow): Attribute[] {
	const e = row.entry;
	const list: Attribute[] = [
		{ key: "timestamp", value: new Date(row.ts).toISOString() },
		{ key: "level", value: row.level, field: "level", column: "level" },
		{ key: "resource", value: row.resourceName, field: "resource", column: "resource" },
		{ key: "replica", value: row.pod, field: "replica", column: "replica" },
		{ key: "region", value: row.region, field: "region", column: "region" },
		{ key: "trace_id", value: e.traceId, column: "trace" },
		{ key: "span_id", value: e.spanId },
	];
	for (const [key, value] of Object.entries(rowAttributes(row))) {
		if (key === "k8s.pod.name" || key === "loco.region") continue;
		list.push({ key, value, column: key });
	}
	return list.filter((a) => a.value !== "");
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
								<DropdownMenuItem render={<Link to={resourcePath(orgId, workspaceId, known.id)} />}>
									<BoxIcon />
									Open {known.name}
								</DropdownMenuItem>
							)}
							<DropdownMenuItem
								disabled={e.traceId === ""}
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
								{e.traceId === "" ? "No trace on this line" : "View trace"}
							</DropdownMenuItem>
							<DropdownMenuItem
								onClick={() => {
									goTo("logs", { range: aroundWindow(row.ts, Date.now()), focusTs: row.ts });
								}}
							>
								<ClockIcon />
								Logs around this line
							</DropdownMenuItem>
							{row.pod !== "" && (
								<DropdownMenuItem
									onClick={() => {
										addTok({ neg: false, key: "replica", value: row.pod });
									}}
								>
									<FilterIcon />
									Only replica {row.replica}
								</DropdownMenuItem>
							)}
							<DropdownMenuItem
								onClick={() => {
									goTo("metrics", {
										range: aroundWindow(row.ts, Date.now()),
										tokens: [{ neg: false, key: "resource", value: row.resourceName }],
										focusTs: row.ts,
									});
								}}
							>
								<ChartLineIcon />
								Metrics around this line
							</DropdownMenuItem>
							<DropdownMenuItem disabled>
								<ScrollTextIcon />
								Logs in this trace
								<span className="ml-auto">
									<SoonTag />
								</span>
							</DropdownMenuItem>
							<DropdownMenuSeparator />
							<DropdownMenuItem
								onClick={() => {
									copy("msg", msgText);
								}}
							>
								<CopyIcon />
								Copy message
							</DropdownMenuItem>
							<DropdownMenuItem
								onClick={() => {
									copy("json", json);
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
							copy("msg", msgText);
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
									copy(`a:${a.key}`, a.value);
								}}
								onFilter={(neg) => {
									if (a.field !== undefined) addTok({ neg, key: a.field, value: a.value });
								}}
								onColumn={() => {
									if (a.column !== undefined && !columns.includes(a.column)) onColumns([...columns, a.column]);
								}}
							/>
						))}
					</div>
					<span className="flex items-center gap-1.5 text-[12px] text-fg3">
						Filtering and excluding by other attributes
						<SoonTag />
					</span>
				</div>
				<div className="flex flex-col gap-2">
					<span className="text-[12.5px] text-fg3">JSON</span>
					<CodeBlock language="json">{json}</CodeBlock>
				</div>
			</div>
		</DetailPanel>
	);
}

const EXCLUDABLE = new Set<FieldKey>(["level", "resource", "region"]);

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
	const { key, value, field } = attribute;
	const canInclude = field !== undefined;
	const canExclude = field !== undefined && EXCLUDABLE.has(field);
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
					label={canInclude ? `Only ${key} ${value}` : `Filtering by ${key} isn't supported yet`}
					disabled={!canInclude}
					onClick={() => {
						onFilter(false);
					}}
				>
					<PlusIcon />
				</AttrAction>
				<AttrAction
					label={canExclude ? `Hide ${key} ${value}` : `Excluding ${key} isn't supported yet`}
					disabled={!canExclude}
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
