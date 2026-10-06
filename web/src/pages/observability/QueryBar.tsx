import { useRef, useState, type ReactNode } from "react";
import { CornerDownRightIcon, HistoryIcon, SearchIcon, StarIcon, TagIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import { formatCount } from "@/lib/format";
import { cn } from "@/lib/utils";

import { useObs } from "./context";
import { levelStyle } from "./format";
import {
	FIELDS,
	fieldDef,
	isFieldKey,
	levelOf,
	parseQuery,
	queryStr,
	readSaved,
	sameToken,
	tokenSupport,
	writeSaved,
	type FieldKey,
	type SavedSearches,
	type Token,
} from "./query";
import { Dot } from "./shared";

interface Suggestion {
	key: string;
	icon: ReactNode;
	iconClass: string;
	pre: string;
	main: string;
	post: string;
	hint: string;
	disabled?: boolean;
	apply: () => void;
	remove?: () => void;
}

interface Group {
	title: string;
	items: Suggestion[];
}

const MONO = "font-mono text-[12.5px]";
const VALUE_WORD_RE = /^(-?)@(\w+):(.*)$/;
const KEY_WORD_RE = /^(-?)@(\w*)$/;
const COMPLETED_TOKEN_RE = /(^|\s)(-?)@(\w+):(\S+)\s$/;

export function QueryBar({ valueCounts }: { valueCounts: (key: FieldKey) => [string, number][] }) {
	const { tokens, setTokens, text, setText, setAppliedText, resourceByName } = useObs();
	const [open, setOpen] = useState(false);
	const [hi, setHi] = useState(0);
	const [saved, setSaved] = useState<SavedSearches>(readSaved);
	const inputRef = useRef<HTMLInputElement>(null);
	const blurTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

	const support = tokenSupport(tokens);
	const words = text.split(" ");
	const cur = words.at(-1) ?? "";
	const focusInput = () => {
		setTimeout(() => {
			inputRef.current?.focus();
		}, 0);
	};

	const updateSaved = (next: SavedSearches) => {
		writeSaved(next);
		setSaved(next);
	};

	const setQ = (nextTokens: Token[], nextText: string) => {
		setTokens(nextTokens);
		setText(nextText);
		setHi(0);
	};

	const addToken = (t: Token) => {
		const rest = text.split(" ");
		rest.pop();
		const exists = tokens.some((x) => sameToken(x, t));
		setQ(exists ? tokens : [...tokens, t], rest.join(" ") + (rest.length > 0 ? " " : ""));
		setOpen(true);
		focusInput();
	};

	const commit = () => {
		const p = parseQuery(text);
		const nextTokens = [...tokens, ...p.tokens];
		const q = queryStr(nextTokens, p.text);
		if (q !== "") updateSaved({ ...saved, recent: [q, ...saved.recent.filter((x) => x !== q)].slice(0, 8) });
		setQ(nextTokens, p.text);
		setAppliedText(p.text);
		setOpen(false);
	};

	const loadQuery = (q: string) => {
		const p = parseQuery(q);
		setQ(p.tokens, p.text);
		setAppliedText(p.text);
		setOpen(false);
	};

	const keyItem = (k: FieldKey, neg: boolean): Suggestion => {
		const def = fieldDef(k);
		const disabled = def?.supported !== true;
		return {
			key: `f:${k}:${String(neg)}`,
			icon: <TagIcon className="size-3.5" />,
			iconClass: "text-fg3",
			pre: `${neg ? "-" : ""}@`,
			main: k,
			post: ":",
			hint: def?.info ?? "",
			disabled,
			apply: () => {
				const w = text.split(" ");
				w.pop();
				setText([...w, `${neg ? "-" : ""}@${k}:`].join(" "));
				setHi(0);
				setOpen(true);
				focusInput();
			},
		};
	};

	const groups: Group[] = [];
	const vm = VALUE_WORD_RE.exec(cur);
	const km = KEY_WORD_RE.exec(cur);
	const vmKey = vm?.[2] ?? "";
	if (vm && isFieldKey(vmKey)) {
		const neg = vm[1] === "-";
		const pref = (vm[3] ?? "").toLowerCase();
		const def = fieldDef(vmKey);
		const vals = def?.supported === true ? valueCounts(vmKey).filter(([v]) => v.toLowerCase().startsWith(pref)).slice(0, 10) : [];
		groups.push({
			title: `${neg ? "Exclude " : ""}${vmKey}`,
			items: vals.map(([v, c]) => {
				const res = resourceByName.get(v);
				const icon =
					vmKey === "level" ? (
						<span className={cn("size-2 rounded-full", levelStyle(levelOf(v)).dot)} />
					) : vmKey === "resource" && res !== undefined ? (
						<Dot size={8} color={res.color} />
					) : (
						<CornerDownRightIcon className="size-3.5" />
					);
				return {
					key: `v:${v}`,
					icon,
					iconClass: "text-fg3",
					pre: `${neg ? "-" : ""}@${vmKey}:`,
					main: v,
					post: "",
					hint: c > 0 ? formatCount(c) : "",
					apply: () => {
						addToken({ neg, key: vmKey, value: v });
					},
				};
			}),
		});
	} else if (km) {
		const neg = km[1] === "-";
		const ks = FIELDS.filter((f) => f.key.startsWith((km[2] ?? "").toLowerCase()));
		groups.push({ title: "Fields", items: ks.map((f) => keyItem(f.key, neg)) });
	} else if (cur.trim() === "") {
		const curQ = queryStr(tokens, text);
		if (saved.pinned.length > 0) {
			groups.push({
				title: "Pinned",
				items: saved.pinned.map((q) => ({
					key: `p:${q}`,
					icon: <StarIcon className="size-3.5" />,
					iconClass: "text-warn",
					pre: "",
					main: q,
					post: "",
					hint: "",
					apply: () => {
						loadQuery(q);
					},
					remove: () => {
						updateSaved({ ...saved, pinned: saved.pinned.filter((x) => x !== q) });
					},
				})),
			});
		}
		const rec = saved.recent.filter((q) => q !== curQ);
		if (rec.length > 0) {
			groups.push({
				title: "Recent",
				items: rec.map((q) => ({
					key: `r:${q}`,
					icon: <HistoryIcon className="size-3.5" />,
					iconClass: "text-fg3",
					pre: "",
					main: q,
					post: "",
					hint: "",
					apply: () => {
						loadQuery(q);
					},
					remove: () => {
						updateSaved({ ...saved, recent: saved.recent.filter((x) => x !== q) });
					},
				})),
			});
		}
		groups.push({ title: "Fields", items: FIELDS.map((f) => keyItem(f.key, false)) });
	} else {
		const ks = FIELDS.filter((f) => f.key.startsWith(cur.toLowerCase()));
		if (ks.length > 0) groups.push({ title: "Fields", items: ks.map((f) => keyItem(f.key, false)) });
	}

	const flat = groups.flatMap((g) => g.items).filter((s) => s.disabled !== true);
	const hiIdx = Math.min(hi, Math.max(0, flat.length - 1));
	const highlighted = flat[hiIdx];
	const curQuery = queryStr(tokens, text);
	const pinned = curQuery !== "" && saved.pinned.includes(curQuery);
	const showMenu = open && (groups.some((g) => g.items.length > 0) || cur !== "");
	const noMatches = open && flat.length === 0 && cur !== "";

	const onChange = (v: string) => {
		const m = COMPLETED_TOKEN_RE.exec(v);
		const key = m?.[3] ?? "";
		if (m && isFieldKey(key)) {
			const t: Token = { neg: m[2] === "-", key, value: m[4] ?? "" };
			setQ([...tokens, t], v.slice(0, v.length - m[0].length + (m[1] ?? "").length));
			setOpen(true);
			return;
		}
		setText(v);
		setHi(0);
		setOpen(true);
	};

	const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
		switch (e.key) {
			case "ArrowDown": {
				e.preventDefault();
				setHi(Math.min(flat.length - 1, hiIdx + 1));
				setOpen(true);
				return;
			}
			case "ArrowUp": {
				e.preventDefault();
				setHi(Math.max(0, hiIdx - 1));
				return;
			}
			case "Enter": {
				e.preventDefault();
				const typingWord = cur.trim() !== "";
				const emptyQuery = text.trim() === "" && tokens.length === 0;
				if (open && highlighted !== undefined && (typingWord || emptyQuery)) highlighted.apply();
				else commit();
				return;
			}
			case "Tab": {
				if (open && highlighted !== undefined && cur.trim() !== "") {
					e.preventDefault();
					highlighted.apply();
				}
				return;
			}
			case "Escape": {
				setOpen(false);
				return;
			}
			case "Backspace": {
				if (text === "" && tokens.length > 0) setQ(tokens.slice(0, -1), text);
				return;
			}
			default:
				return;
		}
	};

	let n = 0;
	return (
		<div className="relative min-w-0 flex-1">
			<div
				onClick={focusInput}
				className={cn(
					"flex min-h-[38px] cursor-text flex-wrap items-center gap-1 rounded-lg border bg-background py-[5px] pr-1.5 pl-2.5",
					open ? "border-primary shadow-[0_0_0_3px_color-mix(in_oklab,var(--accent)_12%,transparent)]" : "border-line",
				)}
			>
				<SearchIcon className="mr-0.5 size-3.5 text-fg3" />
				{tokens.map((t, i) => (
					<TokenChip
						key={`${String(i)}-${t.key}-${t.value}`}
						token={t}
						supported={support[i] === true}
						color={t.key === "resource" ? resourceByName.get(t.value)?.color : undefined}
						onRemove={() => {
							setQ(
								tokens.filter((_, j) => j !== i),
								text,
							);
						}}
					/>
				))}
				<input
					ref={inputRef}
					value={text}
					onChange={(e) => {
						onChange(e.target.value);
					}}
					onKeyDown={onKeyDown}
					onFocus={() => {
						if (blurTimer.current !== null) clearTimeout(blurTimer.current);
						setOpen(true);
					}}
					onBlur={() => {
						blurTimer.current = setTimeout(() => {
							setOpen(false);
						}, 120);
					}}
					placeholder={tokens.length > 0 ? "" : "Search logs, or type @ to filter"}
					autoComplete="off"
					spellCheck={false}
					className={cn("h-[26px] min-w-[180px] flex-1 border-0 bg-transparent text-foreground outline-none placeholder:text-fg4", MONO)}
				/>
				{curQuery !== "" && (
					<Button
						variant="ghost"
						size="icon-xs"
						title="Clear"
						onClick={(e) => {
							e.stopPropagation();
							setQ([], "");
							setAppliedText("");
							focusInput();
						}}
						className="size-[26px] text-fg3 hover:text-foreground"
					>
						<XIcon className="size-3.5" />
					</Button>
				)}
				<Button
					variant="ghost"
					size="icon-xs"
					title={pinned ? "Unpin search" : "Pin search"}
					disabled={curQuery === ""}
					onClick={(e) => {
						e.stopPropagation();
						updateSaved({
							...saved,
							pinned: pinned ? saved.pinned.filter((x) => x !== curQuery) : [curQuery, ...saved.pinned].slice(0, 10),
						});
					}}
					className={cn("size-[26px]", pinned ? "text-warn hover:text-warn" : "text-fg3")}
				>
					<StarIcon className={cn("size-[15px]", pinned && "fill-current")} />
				</Button>
			</div>
			{showMenu && (
				<div
					onMouseDown={(e) => {
						e.preventDefault();
					}}
					className="absolute top-[calc(100%+4px)] right-0 left-0 z-50 max-h-[380px] overflow-y-auto rounded-lg border border-line bg-popover p-1 shadow-popover"
				>
					{groups.map((g) =>
						g.items.length === 0 ? null : (
							<div key={g.title}>
								<div className="px-2.5 pt-2 pb-1 text-xs font-semibold tracking-[0.02em] text-fg3">{g.title}</div>
								{g.items.map((s) => {
									const idx = s.disabled === true ? -1 : n++;
									return (
										<div
											key={s.key}
											onMouseDown={(e) => {
												e.preventDefault();
												if (s.disabled !== true) s.apply();
											}}
											onMouseEnter={() => {
												if (idx >= 0 && hi !== idx) setHi(idx);
											}}
											className={cn(
												"flex h-8 items-center gap-2.5 rounded-sm px-2.5",
												s.disabled === true ? "cursor-not-allowed text-fg4" : "cursor-pointer",
												idx === hiIdx && idx >= 0 && "bg-bg3",
											)}
										>
											<span className={cn("flex", s.iconClass)}>{s.icon}</span>
											<span className={cn("min-w-0 flex-1 truncate", MONO)}>
												<span className="text-fg3">{s.pre}</span>
												<span className={cn("font-semibold", s.disabled === true ? "text-fg4" : "text-foreground")}>{s.main}</span>
												<span className="text-fg3">{s.post}</span>
											</span>
											<span className="text-sm whitespace-nowrap text-fg3">{s.hint}</span>
											{s.disabled === true && <SoonTag />}
											{s.remove !== undefined && (
												<Button
													variant="ghost"
													size="icon-xs"
													title="Remove"
													onMouseDown={(e) => {
														e.preventDefault();
														e.stopPropagation();
														s.remove?.();
													}}
													className="size-[22px] text-fg4 hover:text-foreground"
												>
													<XIcon className="size-[11px]" />
												</Button>
											)}
										</div>
									);
								})}
							</div>
						),
					)}
					{noMatches && <div className="p-2.5 text-fg3">No matches</div>}
				</div>
			)}
		</div>
	);
}

function TokenChip({
	token,
	supported,
	color,
	onRemove,
}: {
	token: Token;
	supported: boolean;
	color: string | undefined;
	onRemove: () => void;
}) {
	const neg = token.neg;
	const levelDot = token.key === "level" ? levelStyle(levelOf(token.value)).dot : null;
	return (
		<span
			title={supported ? undefined : "This filter isn't supported yet and is ignored"}
			className={cn(
				"inline-flex h-[26px] items-center gap-[5px] rounded-[5px] pr-[3px] pl-2 text-[12.5px] whitespace-nowrap",
				neg
					? "bg-bad-bg shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--err)_28%,transparent),inset_3px_0_0_var(--err)]"
					: "bg-[color-mix(in_oklab,var(--accent)_7%,var(--bg))] shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--accent)_22%,transparent),inset_3px_0_0_var(--accent)]",
				!supported && "line-through opacity-55",
			)}
		>
			<span className={cn("font-mono text-sm", neg ? "text-bad-fg" : "text-info-fg")}>
				{neg ? "-" : ""}
				{token.key}:
			</span>
			<span className={cn("inline-flex items-center gap-[5px] font-mono text-sm font-semibold", neg ? "text-bad-fg" : "text-foreground")}>
				{levelDot !== null && <span className={cn("size-[7px] rounded-[2px]", levelDot)} />}
				{color !== undefined && <Dot color={color} />}
				{token.value}
			</span>
			<Button
				variant="ghost"
				size="icon-xs"
				title="Remove filter"
				onClick={(e) => {
					e.stopPropagation();
					onRemove();
				}}
				className="size-5 text-fg4 hover:bg-line hover:text-foreground"
			>
				<XIcon className="size-[11px]" />
			</Button>
		</span>
	);
}
