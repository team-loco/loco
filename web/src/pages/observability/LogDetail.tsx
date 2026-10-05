import { useState } from "react";
import { ChartLineIcon, CheckIcon, CopyIcon, MinusIcon, PlusIcon, ScrollTextIcon, SearchIcon, ServerIcon, WaypointsIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { Code, CodeBlock } from "@/components/design/CodeBlock";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { cn } from "@/lib/utils";

import { fitRange, useObs } from "./context";
import { bodyParts, flattenJson, fmtClock, fmtTs, levelStyle } from "./format";
import { sameToken, type FieldKey, type Token } from "./query";
import type { LogRow } from "./rows";
import { CopyButton, DetailPanel, Dot, JumpButton, PanelNav, useCopy } from "./shared";

type Tab = "details" | "json";

const ATTR_FIELD: Record<string, FieldKey> = {
	level: "level",
	resource: "resource",
	replica: "replica",
	"k8s.pod.name": "replica",
	region: "region",
};

export function LogDetail({
	row,
	words,
	nowMs,
	onPrev,
	onNext,
	onClose,
}: {
	row: LogRow;
	words: string[];
	nowMs: number;
	onPrev: (() => void) | null;
	onNext: (() => void) | null;
	onClose: () => void;
}) {
	const { tokens, setTokens, goTo } = useObs();
	const [tab, setTab] = useState<Tab>("details");
	const [attrQ, setAttrQ] = useState("");
	const [copied, copy] = useCopy();
	const style = levelStyle(row.level);
	const e = row.entry;

	const attrsAll: Record<string, string> = {
		timestamp: new Date(row.ts).toISOString(),
		level: row.level,
		resource: row.resourceName,
		replica: row.pod,
		region: row.region,
		...(e.traceId !== "" ? { trace_id: e.traceId } : {}),
		...(e.spanId !== "" ? { span_id: e.spanId } : {}),
		...e.resourceAttributes,
		...e.logAttributes,
		...(row.json !== null ? flattenJson(row.json) : {}),
	};
	const aq = attrQ.toLowerCase();
	const attrs = Object.entries(attrsAll).filter(
		([k, v]) => v !== "" && (aq === "" || k.toLowerCase().includes(aq) || v.toLowerCase().includes(aq)),
	);

	const addTok = (t: Token) => {
		if (tokens.some((x) => sameToken(x, t))) return;
		setTokens([...tokens, t]);
	};

	const json = JSON.stringify(
		{
			timestamp: attrsAll.timestamp,
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
	const range = fitRange(row.ts, nowMs);

	return (
		<DetailPanel
			header={
				<>
					<span
						className={cn(
							"inline-flex h-5 w-12 items-center justify-center rounded-[3px] text-[10.5px] font-semibold tracking-[0.02em]",
							style.badge,
						)}
					>
						{row.label}
					</span>
					<span className="flex min-w-0 items-center gap-1.5 font-semibold">
						<Dot size={8} color={row.color} />
						<span className="truncate">{row.resourceName}</span>
					</span>
					<span className="text-sm whitespace-nowrap text-fg3 tabular-nums">{fmtTs(row.ts, true)}</span>
					<div className="flex-1" />
					<PanelNav onPrev={onPrev} onNext={onNext} onClose={onClose} />
				</>
			}
		>
			<div className="relative border-b border-line p-3.5">
				<Button
					variant="ghost"
					size="icon-xs"
					title="Copy message"
					onClick={() => {
						copy("msg", msgText);
					}}
					className={cn("absolute top-2.5 right-2.5", copied === "msg" ? "text-ok-fg" : "text-fg3")}
				>
					{copied === "msg" ? <CheckIcon className="size-3" /> : <CopyIcon className="size-3" />}
				</Button>
				<div
					className={cn(
						"mr-6 max-h-[280px] overflow-auto rounded-sm font-mono text-[12.5px] leading-[1.55] break-words whitespace-pre-wrap",
						row.json !== null ? "bg-bg2 px-3 py-2.5 text-foreground" : style.body,
					)}
				>
					{row.json !== null ? <Code code={msgText} language="json" /> : bodyParts(e.body, words)}
				</div>
			</div>
			<ToggleGroup
				variant="segmented"
				value={[tab]}
				onValueChange={(v: string[]) => {
					if (v[0] === "details" || v[0] === "json") setTab(v[0]);
				}}
				className="mx-3.5 mt-2.5 flex w-auto"
			>
				<ToggleGroupItem value="details" className="h-[26px]! flex-1 text-[12.5px]">
					Details
				</ToggleGroupItem>
				<ToggleGroupItem value="json" className="h-[26px]! flex-1 text-[12.5px]">
					JSON
				</ToggleGroupItem>
			</ToggleGroup>
			<div className="flex flex-1 flex-col gap-4 overflow-y-auto px-3.5 pt-3 pb-4">
				{tab === "details" ? (
					<>
						<div className="flex flex-col gap-1.5">
							<JumpButton
								icon={<WaypointsIcon />}
								label="View trace"
								hint={e.traceId !== "" ? e.traceId.slice(0, 8) : undefined}
								soon
							/>
							<JumpButton icon={<ScrollTextIcon />} label="Logs in this trace" soon />
							<JumpButton
								icon={<ServerIcon />}
								label={`Logs from ${row.pod || row.resourceName}`}
								hint="replica"
								onClick={
									row.pod === ""
										? undefined
										: () => {
												setTokens([
													{ neg: false, key: "resource", value: row.resourceName },
													{ neg: false, key: "replica", value: row.pod },
												]);
											}
								}
							/>
							<JumpButton
								icon={<ChartLineIcon />}
								label={`Metrics at ${fmtClock(row.ts)}`}
								hint={row.resourceName}
								onClick={() => {
									goTo("metrics", { range, resource: row.resourceName, focusTs: row.ts });
								}}
							/>
						</div>
						<div className="flex flex-col gap-2">
							<div className="flex h-[30px] items-center gap-2 rounded-sm border border-line px-2">
								<SearchIcon className="size-3.5 text-fg3" />
								<input
									value={attrQ}
									onChange={(ev) => {
										setAttrQ(ev.target.value);
									}}
									placeholder="Filter attributes"
									className="min-w-0 flex-1 border-0 bg-transparent text-[12.5px] text-foreground outline-none placeholder:text-fg4"
								/>
							</div>
							<div className="flex flex-col overflow-hidden rounded-sm border border-line">
								{attrs.length === 0 && <div className="px-2.5 py-2 text-sm text-fg3">No attributes match.</div>}
								{attrs.map(([k, v]) => {
									const f = ATTR_FIELD[k];
									const fv = k === "level" ? row.level : v;
									const ck = `a:${k}`;
									return (
										<div
											key={k}
											className="grid min-h-[30px] grid-cols-[128px_minmax(0,1fr)_auto] items-center gap-2.5 border-b border-line pr-1.5 pl-2.5 font-mono text-sm last:border-b-0 hover:bg-bg2"
										>
											<span className="truncate text-fg3" title={k}>
												{k}
											</span>
											<span className={cn("py-[5px] break-all", /^\d+(\.\d+)?$/.test(v) ? "text-info-fg" : "text-foreground")}>
												{v}
											</span>
											<span className="flex gap-0.5">
												{f !== undefined && (
													<>
														<Button
															variant="ghost"
															size="icon-xs"
															title="Filter by this value"
															onClick={() => {
																addTok({ neg: false, key: f, value: fv });
															}}
															className="size-[22px] text-fg4 hover:text-foreground"
														>
															<PlusIcon className="size-3" />
														</Button>
														<Button
															variant="ghost"
															size="icon-xs"
															title={f === "replica" ? "Excluding a replica isn't supported yet" : "Exclude this value"}
															disabled={f === "replica"}
															onClick={() => {
																addTok({ neg: true, key: f, value: fv });
															}}
															className="size-[22px] text-fg4 hover:text-foreground"
														>
															<MinusIcon className="size-3" />
														</Button>
													</>
												)}
												<CopyButton
													copied={copied === ck}
													onCopy={() => {
														copy(ck, v);
													}}
													title="Copy value"
												/>
											</span>
										</div>
									);
								})}
							</div>
						</div>
					</>
				) : (
					<div className="relative">
						<Button
							variant="outline"
							size="xs"
							onClick={() => {
								copy("json", json);
							}}
							className="absolute top-1.5 right-1.5 h-[26px] px-2 text-sm text-fg2"
						>
							{copied === "json" ? <CheckIcon className="size-3" /> : <CopyIcon className="size-3" />}
							{copied === "json" ? "Copied" : "Copy"}
						</Button>
						<CodeBlock
							language="json"
							className="rounded-sm bg-bg2"
							codeClassName="p-3 break-all whitespace-pre-wrap"
						>
							{json}
						</CodeBlock>
					</div>
				)}
			</div>
		</DetailPanel>
	);
}
