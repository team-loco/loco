import { useEffect, useRef, useState, type ReactNode } from "react";
import { HistoryIcon, SearchIcon, StarIcon, TagIcon, XIcon } from "lucide-react";

import {
	Combobox,
	ComboboxChipsInput,
	ComboboxContent,
	ComboboxList,
	ComboboxItem,
	ComboboxGroup,
	ComboboxLabel,
	useComboboxAnchor,
} from "@/components/design/Combobox";
import { Button } from "@/components/design/Button";
import { Kbd } from "@/components/design/Kbd";
import { SoonTag } from "@/components/design/SoonTag";
import { formatCount } from "@/lib/format";
import { useStoredJSON } from "@/lib/storage";
import { cn } from "@/lib/utils";

import { useObs } from "./context";
import { levelStyle } from "./format";
import {
	addRecent,
	agoShort,
	fieldDef,
	isFieldKey,
	levelOf,
	parseQuery,
	PINNED_KEY,
	pinnedQueries,
	queryStr,
	recentKey,
	recentQueries,
	recentQuery,
	sameToken,
	searchesText,
	shownRecent,
	tokenSupport,
	viewFields,
	type FieldDef,
	type FieldKey,
	type Token,
} from "./query";
import { Dot } from "./shared";

interface Suggestion {
	key: string;
	icon: ReactNode;
	main: string;
	hint: string;
	disabled?: boolean;
	apply: () => void;
	remove?: () => void;
}

interface Group {
	title: string;
	action?: ReactNode;
	items: Suggestion[];
}

const VALUE_WORD_RE = /^(-?)@(\w+):(.*)$/;
const KEY_WORD_RE = /^(-?)@(\w*)$/;
const COMPLETED_TOKEN_RE = /(^|\s)(-?)@(\w+):(\S+)\s$/;
const MAX_VALUES = 10;
const MAX_PINNED = 10;
const RECENT_SAVE_MS = 1500;

export type ValueCounts = (key: FieldKey) => [string, number][];

function placeholder(view: string): string {
	switch (view) {
		case "logs":
			return "Search messages, or type @ to filter by resource, level, replica…";
		case "traces":
			return "Search span names, or type @ to filter by resource, status, trace…";
		case "events":
			return "Search events, or type @ to filter by resource or type";
		default:
			return "Type @ to filter by resource or region";
	}
}

export function QueryBar({ valueCounts }: { valueCounts: ValueCounts }) {
	const { orgId, workspaceId, view, tokens, text: applied, setQuery, resourceByName } = useObs();
	const [text, setText] = useState(applied);
	const [synced, setSynced] = useState(applied);
	if (synced !== applied) {
		setSynced(applied);
		setText(applied);
	}
	const [open, setOpen] = useState(false);
	const [now, setNow] = useState(Date.now);
	const [highlighted, setHighlighted] = useState<Suggestion | undefined>();
	const anchor = useComboboxAnchor();
	const [pinnedList, updatePinned] = useStoredJSON(PINNED_KEY, pinnedQueries);
	const scope = recentKey(orgId, workspaceId, view);
	const [recent, updateRecent] = useStoredJSON(scope, recentQueries);
	const inputRef = useRef<HTMLInputElement>(null);

	const appliedRecent = recentQuery(view, tokens, applied);
	useEffect(() => {
		if (appliedRecent === "") return;
		const timer = setTimeout(() => {
			updateRecent((list) => addRecent(list, appliedRecent, Date.now()));
		}, RECENT_SAVE_MS);
		return () => {
			clearTimeout(timer);
		};
	}, [appliedRecent, updateRecent]);

	const fields = viewFields(view);
	const support = tokenSupport(view, tokens);
	const textIgnored = !searchesText(view) && applied !== "";
	const words = text.split(" ");
	const cur = words.at(-1) ?? "";
	const focusInput = () => {
		inputRef.current?.focus();
	};

	const setTokens = (next: Token[], nextText = text) => {
		setText(nextText);
		setQuery(next, applied);
	};

	const addToken = (t: Token) => {
		const rest = text.split(" ");
		rest.pop();
		const exists = tokens.some((x) => sameToken(x, t));
		setTokens(exists ? tokens : [...tokens, t], rest.join(" ") + (rest.length > 0 ? " " : ""));
		setOpen(true);
		focusInput();
	};

	const commit = () => {
		const p = parseQuery(text);
		const nextTokens = [...tokens, ...p.tokens];
		setText(p.text);
		setQuery(nextTokens, p.text);
		setOpen(false);
	};

	const loadQuery = (q: string) => {
		const p = parseQuery(q);
		setText(p.text);
		setQuery(p.tokens, p.text);
		setOpen(false);
	};

	const keyItem = (def: FieldDef, neg: boolean): Suggestion => ({
		key: `f:${def.key}:${String(neg)}`,
		icon: <TagIcon className="size-3.5" />,
		main: `${neg ? "-" : ""}@${def.key}`,
		hint: def.label,
		disabled: !def.views.includes(view),
		apply: () => {
			const w = text.split(" ");
			w.pop();
			setText([...w, `${neg ? "-" : ""}@${def.key}:`].join(" "));
			setOpen(true);
			focusInput();
		},
	});

	const groups: Group[] = [];
	const vm = VALUE_WORD_RE.exec(cur);
	const km = KEY_WORD_RE.exec(cur);
	const vmKey = vm?.[2] ?? "";
	if (vm && isFieldKey(vmKey)) {
		const neg = vm[1] === "-";
		const part = vm[3] ?? "";
		const pref = part.toLowerCase();
		const def = fieldDef(vmKey);
		const vals = def.views.includes(view)
			? valueCounts(vmKey)
					.filter(([v]) => v.toLowerCase().includes(pref))
					.slice(0, MAX_VALUES)
			: [];
		const typed: [string, number][] = part !== "" && !vals.some(([v]) => v.toLowerCase() === pref) ? [[part, 0]] : [];
		const items: Suggestion[] = [...typed, ...vals].map(([v, c]) => {
			const res = resourceByName.get(v);
			const icon =
				vmKey === "level" ? (
					<span className={cn("size-2 rounded-full", levelStyle(levelOf(v)).dot)} />
				) : vmKey === "resource" && res !== undefined ? (
					<Dot size={8} color={res.color} />
				) : (
					<TagIcon className="size-3.5" />
				);
			return {
				key: `v:${v}`,
				icon,
				main: `${neg ? "-" : ""}@${vmKey}:${v}`,
				hint: c > 0 ? formatCount(c) : "",
				apply: () => {
					addToken({ neg, key: vmKey, value: v });
				},
			};
		});
		groups.push({ title: `@${vmKey} values`, items });
	} else if (km) {
		const neg = km[1] === "-";
		const ks = fields.filter((f) => f.key.startsWith((km[2] ?? "").toLowerCase()));
		groups.push({ title: "Attributes", items: ks.map((f) => keyItem(f, neg)) });
	} else if (cur.trim() === "") {
		if (pinnedList.length > 0) {
			groups.push({
				title: "Pinned",
				items: pinnedList.map((q) => ({
					key: `p:${q}`,
					icon: <StarIcon className="size-3.5 text-warn" />,
					main: q,
					hint: "",
					apply: () => {
						loadQuery(q);
					},
					remove: () => {
						updatePinned((list) => list.filter((x) => x !== q));
					},
				})),
			});
		}
		const rec = shownRecent(recent, recentQuery(view, tokens, text));
		if (scope !== null && rec.length > 0) {
			groups.push({
				title: "Recent",
				action: (
					<Button
						variant="link"
						onClick={() => {
							updateRecent(() => null);
							focusInput();
						}}
						className="h-auto p-0 text-[11.5px] text-fg3"
					>
						Clear
					</Button>
				),
				items: rec.map((r) => ({
					key: `r:${r.q}`,
					icon: <HistoryIcon className="size-3.5" />,
					main: r.q,
					hint: agoShort(r.ts, now),
					apply: () => {
						loadQuery(r.q);
					},
				})),
			});
		}
		groups.push({ title: "Filter by an attribute", items: fields.map((f) => keyItem(f, false)) });
	} else {
		if (searchesText(view)) {
			groups.push({
				title: "Search",
				items: [
					{
						key: "search",
						icon: <SearchIcon className="size-3.5" />,
						main: `“${text.trim()}”`,
						hint: "enter",
						apply: commit,
					},
				],
			});
		}
		const ks = fields.filter((f) => f.key.startsWith(cur.toLowerCase()));
		if (ks.length > 0) groups.push({ title: "Attributes", items: ks.map((f) => keyItem(f, false)) });
	}

	const flat = groups.flatMap((g) => g.items).filter((s) => s.disabled !== true);
	const curQuery = queryStr(tokens, text);
	const pinned = curQuery !== "" && pinnedList.includes(curQuery);
	const noMatches = open && flat.length === 0 && cur !== "";

	const onChange = (v: string) => {
		const m = COMPLETED_TOKEN_RE.exec(v);
		const key = m?.[3] ?? "";
		if (m && isFieldKey(key)) {
			const t: Token = { neg: m[2] === "-", key, value: m[4] ?? "" };
			setTokens([...tokens, t], v.slice(0, v.length - m[0].length + (m[1] ?? "").length));
			setOpen(true);
			return;
		}
		setText(v);
		setOpen(true);
	};

	const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
		if (e.nativeEvent.isComposing) return;
		if (e.key === "Enter" && (!open || highlighted === undefined || (cur.trim() === "" && tokens.length > 0))) {
			e.preventDefault();
			commit();
		} else if (e.key === "Tab" && open && highlighted !== undefined && cur.trim() !== "") {
			e.preventDefault();
			highlighted.apply();
		} else if (e.key === "Backspace" && text === "" && tokens.length > 0) {
			setTokens(tokens.slice(0, -1));
		}
	};

	return (
		<Combobox
			items={groups.flatMap((group) => group.items)}
			filter={null}
			value={null}
			inputValue={text}
			itemToStringLabel={(item: Suggestion) => item.main}
			autoHighlight
			onInputValueChange={(value, details) => {
				if (details.reason === "input-change") onChange(value);
			}}
			onValueChange={(item) => {
				item?.apply();
			}}
			onItemHighlighted={setHighlighted}
			open={open}
			onOpenChange={(next, details) => {
				if (details.reason !== "item-press") setOpen(next);
			}}
		>
			<div ref={anchor} className="relative min-w-0">
				<div
					role="presentation"
					onClick={focusInput}
					className="flex min-h-[42px] cursor-text flex-wrap items-center gap-1.5 rounded-lg border border-line2 bg-raise py-[5px] pr-1.5 pl-3 focus-within:border-fg3 focus-within:shadow-[0_0_0_3px_color-mix(in_oklab,var(--fg)_7%,transparent)]"
				>
					<SearchIcon className="mr-0.5 size-3.5 shrink-0 text-fg3" />
					{tokens.map((t, i) => (
						<TokenChip
							key={`${String(i)}-${t.key}-${t.value}`}
							token={t}
							reason={support[i]?.ok === true ? null : (support[i]?.reason ?? null)}
							color={t.key === "resource" ? resourceByName.get(t.value)?.color : undefined}
							onFlip={() => {
								setTokens(tokens.map((x, j) => (j === i ? { ...x, neg: !x.neg } : x)));
							}}
							onRemove={() => {
								setTokens(tokens.filter((_, j) => j !== i));
							}}
						/>
					))}
					<ComboboxChipsInput
						ref={inputRef}
						onKeyDown={onKeyDown}
						onFocus={() => {
							setNow(Date.now());
							setOpen(true);
						}}
						placeholder={tokens.length > 0 ? "" : placeholder(view)}
						aria-label="Search and filter"
						aria-describedby={textIgnored ? "omni-text-ignored" : undefined}
						autoComplete="off"
						spellCheck={false}
						className={cn(
							"h-[30px] min-w-[200px] flex-1 border-0 bg-transparent font-mono text-[12.5px] text-foreground outline-none placeholder:font-sans placeholder:text-[13px] placeholder:text-fg4",
							textIgnored && "text-fg3 line-through",
						)}
					/>
					{textIgnored && (
						<span id="omni-text-ignored" className="text-[12px] text-fg3">
							Text search doesn&apos;t filter {view}
						</span>
					)}
					{curQuery !== "" && (
						<Button
							variant="link"
							onClick={(e) => {
								e.stopPropagation();
								setText("");
								setQuery([], "");
								focusInput();
							}}
							className="px-1 text-[12px] text-fg3"
						>
							Clear
						</Button>
					)}
					<Button
						variant="ghost"
						size="icon-xs"
						aria-label={pinned ? "Unpin search" : "Pin search"}
						title={pinned ? "Unpin search" : "Pin search"}
						disabled={curQuery === ""}
						onClick={(e) => {
							e.stopPropagation();
							updatePinned((list) =>
								pinned ? list.filter((x) => x !== curQuery) : [curQuery, ...list].slice(0, MAX_PINNED),
							);
						}}
						className={cn(pinned ? "text-warn hover:text-warn" : "text-fg3")}
					>
						<StarIcon className={cn("size-[15px]", pinned && "fill-current")} />
					</Button>
				</div>
				<ComboboxContent anchor={anchor} className="w-[min(560px,var(--anchor-width))] min-w-0 p-1.5">
					<ComboboxList aria-label="Suggestions">
						{groups
							.filter((g) => g.items.length > 0)
							.map((g, gi) => (
								<ComboboxGroup
									key={g.title}
									className={cn("flex flex-col", gi > 0 && "mt-1 border-t border-line pt-1")}
								>
									<ComboboxLabel className="flex items-center justify-between px-2 pt-1.5 pb-1 text-[11.5px] text-fg3">
										{g.title}
										{g.action}
									</ComboboxLabel>
									{g.items.map((s) => (
										<ComboboxItem key={s.key} value={s} disabled={s.disabled}>
											<span className="flex text-fg3">{s.icon}</span>
											<span className="min-w-0 flex-1 truncate font-mono">{s.main}</span>
											<span className="text-[12px] whitespace-nowrap text-fg3">{s.hint}</span>
											{s.disabled === true && <SoonTag />}
											{s.remove !== undefined && (
												<Button
													variant="ghost"
													size="icon-xs"
													aria-label={`Unpin ${s.main}`}
													onClick={(e) => {
														e.stopPropagation();
														s.remove?.();
														focusInput();
													}}
													className="size-[22px] text-fg4 hover:text-foreground"
												>
													<XIcon className="size-[11px]" />
												</Button>
											)}
										</ComboboxItem>
									))}
								</ComboboxGroup>
							))}
						{noMatches && <div className="p-2.5 text-fg3">No matches</div>}
						<div className="mt-1 flex flex-wrap items-center gap-1.5 border-t border-line px-2 pt-2 pb-1 text-[11.5px] text-fg3">
							<Kbd>↑↓</Kbd> move <Kbd>enter</Kbd> add <Kbd>-@</Kbd> exclude <Kbd>*</Kbd> prefix
						</div>
					</ComboboxList>
				</ComboboxContent>
			</div>
		</Combobox>
	);
}

function TokenChip({
	token,
	reason,
	color,
	onFlip,
	onRemove,
}: {
	token: Token;
	reason: string | null;
	color: string | undefined;
	onFlip: () => void;
	onRemove: () => void;
}) {
	const neg = token.neg;
	const levelDot = token.key === "level" ? levelStyle(levelOf(token.value)).dot : null;
	const label = `${neg ? "Excluding" : "Only"} ${token.key} ${token.value}`;
	return (
		<span
			className={cn(
				"inline-flex h-[26px] items-center rounded-sm bg-panel shadow-[inset_0_0_0_1px_var(--line2)]",
				reason !== null && "opacity-55",
			)}
		>
			<button
				type="button"
				title={reason ?? (neg ? "Excluding. Click to include." : "Click to exclude")}
				aria-label={reason === null ? `${label}. Click to ${neg ? "include" : "exclude"}.` : `${label}. ${reason}.`}
				onClick={(e) => {
					e.stopPropagation();
					onFlip();
				}}
				className={cn(
					"flex h-full items-center gap-[5px] pr-1 pl-2 font-mono text-[12px] focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
					neg ? "text-bad-fg" : "text-foreground",
					reason !== null && "line-through",
				)}
			>
				<span className={neg ? "text-bad-fg" : "text-fg3"}>
					{neg ? "-" : ""}@{token.key}:
				</span>
				{levelDot !== null && <span className={cn("size-[7px] rounded-[2px]", levelDot)} />}
				{color !== undefined && <Dot color={color} />}
				{token.value}
			</button>
			<button
				type="button"
				aria-label={`Remove ${token.key} ${token.value}`}
				onClick={(e) => {
					e.stopPropagation();
					onRemove();
				}}
				className="flex h-full w-5 items-center justify-center rounded-r-sm text-fg3 hover:bg-panel2 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
			>
				<XIcon className="size-3" />
			</button>
		</span>
	);
}
