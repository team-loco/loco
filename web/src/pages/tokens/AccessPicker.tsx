import { ChevronRightIcon, SearchIcon, XIcon } from "lucide-react";
import { useRef, useState } from "react";

import {
	Combobox,
	ComboboxChipsInput,
	ComboboxContent,
	ComboboxEmpty,
	ComboboxGroup,
	ComboboxItem,
	ComboboxLabel,
	ComboboxList,
} from "@/components/design/Combobox";
import { Button } from "@/components/design/Button";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { LEVEL_LABEL } from "@/hooks/useMyScopes";

import { ALLOWS, heldLevel, LEVELS } from "./model";
import type { EntityNode, HeldScopes, Level, NodeKind } from "./model";
import { kindIcon } from "./icons";
import { CHOICE_PAGE, useAccessChoices } from "./useAccessChoices";

export interface AccessItem {
	node: EntityNode;
	level: Level;
}

const KIND_RANK = { user: 0, org: 1, ws: 2, res: 3, system: 4 } as const;
const GROUPS: { label: string; kinds: NodeKind[] }[] = [
	{ label: "Account", kinds: ["user"] },
	{ label: "Organization", kinds: ["org"] },
	{ label: "Workspaces", kinds: ["ws"] },
	{ label: "Resources", kinds: ["res"] },
];

export function AccessPicker({
	roots,
	org,
	held,
	items,
	allowUser,
	scope,
	onChange,
}: {
	roots: EntityNode[];
	org: EntityNode | null;
	held: HeldScopes;
	items: AccessItem[];
	allowUser: boolean;
	scope: EntityNode | null;
	onChange: (items: AccessItem[]) => void;
}) {
	const [q, setQ] = useState("");
	const [focus, setFocus] = useState(false);
	const highlightedRef = useRef<EntityNode | undefined>(undefined);
	const anchorRef = useRef<HTMLDivElement>(null);
	const levelByKey = new Map(items.map((x) => [x.node.key, x.level]));
	const query = q.trim().toLowerCase();
	const choices = useAccessChoices({ org, scope, text: q.trim() });

	const coveredBy = (node: EntityNode, level: Level): string | null => {
		for (let p = node.parent; p !== null; p = p.parent) {
			const itLevel = levelByKey.get(p.key);
			if (itLevel !== undefined && itLevel >= level) return p.label;
		}
		return null;
	};

	const within = (node: EntityNode): boolean => {
		if (scope === null) return true;
		for (let cur: EntityNode | null = node; cur !== null; cur = cur.parent) {
			if (cur.key === scope.key) return true;
		}
		return false;
	};
	const named = (n: EntityNode): boolean => !query.includes("/") && n.label.toLowerCase().includes(query);
	const pool = [...roots.filter(named), ...choices.workspaces, ...choices.resources]
		.filter((n) => (allowUser || n.kind !== "user") && within(n))
		.filter((n) => !levelByKey.has(n.key))
		.toSorted((a, b) => {
			const kind = KIND_RANK[a.kind] - KIND_RANK[b.kind];
			if (kind !== 0 || query === "") return kind;
			const sa = a.label.toLowerCase().startsWith(query) ? 0 : 1;
			const sb = b.label.toLowerCase().startsWith(query) ? 0 : 1;
			return sa - sb;
		});
	const shown = pool.slice(0, CHOICE_PAGE);
	const more = pool.length - shown.length + choices.more;
	const loading = choices.workspacesLoading || choices.resourcesLoading;
	const groups = GROUPS.map((g) => ({ ...g, nodes: shown.filter((n) => g.kinds.includes(n.kind)) })).filter(
		(g) => g.nodes.length > 0,
	);
	const placeholder =
		scope?.kind === "ws"
			? `Add ${scope.label} or one of its resources…`
			: "Add a workspace or resource, e.g. storefront/web";

	const expand = (node: EntityNode) => {
		setQ(`${node.label}/`);
	};

	// Picking a workspace the caller holds no access to lists the resources it may hold grants on.
	const add = (node: EntityNode) => {
		const lvl = heldLevel(held, node);
		if (lvl === 0) {
			if (node.kind === "ws") expand(node);
			return;
		}
		onChange([...items, { node, level: 1 }]);
		setQ("");
	};

	const resourceCount = (n: EntityNode): string | null =>
		n.resources === undefined ? null : `${n.resources.toString()} resources`;

	const resultNote = (n: EntityNode): string => {
		switch (n.kind) {
			case "user":
				return "Your user account";
			case "org":
				return "Organization";
			case "ws": {
				const count = resourceCount(n);
				return count === null ? "Workspace" : `Workspace · ${count}`;
			}
			case "res": {
				const cov = coveredBy(n, 1);
				return cov === null ? (n.parent?.label ?? "Resource") : `Included in ${cov}`;
			}
			case "system":
				return "System";
		}
	};

	const itemKind = (n: EntityNode): string => {
		switch (n.kind) {
			case "user":
				return "User · your profile and account";
			case "org":
				return "Organization · everything in it";
			case "ws": {
				const count = resourceCount(n);
				return `Workspace · all ${count ?? "resources"}`;
			}
			case "res":
				return n.parent?.label ?? "Resource";
			case "system":
				return "System";
		}
	};

	const rows = items.map((it, i) => {
		const n = it.node;
		const lvl = heldLevel(held, n);
		return (
			<div key={n.key} className="flex flex-wrap items-center gap-x-2.5 gap-y-2 border-b border-line py-2 pr-2.5 pl-3">
				<span className="flex shrink-0 text-fg3 [&_svg]:size-3.5">{kindIcon(n.kind)}</span>
				<span className="flex min-w-[9rem] flex-1 flex-col gap-px">
					<span className="truncate font-mono text-[12.5px]">{n.label}</span>
					<span className="text-[11.5px] text-fg3">{itemKind(n)}</span>
				</span>
				<ToggleGroup
					variant="segmented"
					aria-label={`Level for ${n.label}`}
					value={[it.level.toString()]}
					onValueChange={(v: string[]) => {
						const next = LEVELS.find((l) => l.toString() === v[0]);
						if (next === undefined || next > lvl) return;
						onChange(items.map((x, j) => (j === i ? { ...x, level: next } : x)));
					}}
				>
					{LEVELS.map((l) => {
						const dis = l > lvl;
						const tip = dis
							? `You only have ${lvl === 0 ? "no access" : LEVEL_LABEL[lvl]} on ${n.label}`
							: ALLOWS[n.kind].slice(0, l).join(" · ");
						return (
							<ToggleGroupItem key={l} value={l.toString()} disabled={dis} title={tip} className="disabled:opacity-45">
								{LEVEL_LABEL[l]}
							</ToggleGroupItem>
						);
					})}
				</ToggleGroup>
				<Button
					type="button"
					variant="ghost"
					size="icon-xs"
					aria-label={`Remove ${n.label}`}
					className="ml-auto"
					onClick={() => {
						onChange(items.filter((_, j) => j !== i));
					}}
				>
					<XIcon />
				</Button>
			</div>
		);
	});

	return (
		<Combobox
			items={shown}
			filter={null}
			itemToStringLabel={(node: EntityNode) => node.label}
			value={null}
			inputValue={q}
			onInputValueChange={(value, details) => {
				if (details.reason === "input-change") setQ(value);
			}}
			onValueChange={(node) => {
				if (node !== null) add(node);
			}}
			open={focus}
			onOpenChange={setFocus}
			onItemHighlighted={(node) => {
				highlightedRef.current = node;
			}}
			autoHighlight
		>
			<div ref={anchorRef} className={cn("relative rounded-lg border bg-raise", focus ? "border-fg4" : "border-line2")}>
				{items.length === 0 && <div className="p-3 pb-1 text-[12.5px] text-fg3">No permissions yet.</div>}
				{rows}
				<div className="flex h-[38px] items-center gap-2 px-3">
					<SearchIcon className="size-3.5 text-fg3" />
					<ComboboxChipsInput
						onFocus={() => {
							setFocus(true);
						}}
						onKeyDown={(e) => {
							const highlighted = highlightedRef.current;
							const atEnd = e.currentTarget.selectionStart === e.currentTarget.value.length;
							if (e.key === "ArrowRight" && highlighted?.kind === "ws" && atEnd) {
								e.preventDefault();
								expand(highlighted);
								return;
							}
							if (e.key !== "Enter" || highlighted !== undefined) return;
							e.preventDefault();
							e.preventBaseUIHandler();
							const first = shown.find((n) => heldLevel(held, n) > 0);
							if (first !== undefined) add(first);
						}}
						placeholder={placeholder}
						autoComplete="off"
						aria-label="Add a workspace or resource"
						className="min-w-0 flex-1 border-0 bg-transparent text-[13px] text-foreground outline-none placeholder:text-fg4"
					/>
				</div>
				<ComboboxContent anchor={anchorRef}>
					<ComboboxList>
						{groups.map((g) => (
							<ComboboxGroup key={g.label}>
								<ComboboxLabel className="px-2 pt-1.5 pb-1 text-[11.5px] text-fg3">{g.label}</ComboboxLabel>
								{g.nodes.map((n) => {
									const lvl = heldLevel(held, n);
									const noAccess = lvl === 0;
									const disabled = noAccess && n.kind !== "ws";
									const covered = n.kind === "res" && coveredBy(n, 1) !== null;
									return (
										<ComboboxItem
											key={n.key}
											value={n}
											disabled={disabled}
											title={noAccess ? `You have no access to ${n.label}` : undefined}
											className={cn(noAccess && "opacity-45", covered && "opacity-50")}
										>
											<span className="flex text-fg3 [&_svg]:size-3.5">{kindIcon(n.kind)}</span>
											<span className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{n.label}</span>
											<span className="text-[11.5px] whitespace-nowrap text-fg3">
												{noAccess ? "No access" : resultNote(n)}
											</span>
											{n.kind === "ws" && (
												<Button
													type="button"
													variant="ghost"
													size="icon-xs"
													aria-label={`Show resources in ${n.label}`}
													title={`Show resources in ${n.label}`}
													className="-mr-1"
													onPointerDown={(e) => {
														e.preventDefault();
														e.stopPropagation();
													}}
													onMouseDown={(e) => {
														e.preventDefault();
														e.stopPropagation();
													}}
													onClick={(e) => {
														e.preventDefault();
														e.stopPropagation();
														expand(n);
													}}
												>
													<ChevronRightIcon />
												</Button>
											)}
										</ComboboxItem>
									);
								})}
							</ComboboxGroup>
						))}
						{!loading && choices.error === null && (
							<ComboboxEmpty className="p-3 text-[12.5px] text-fg3">
								{query === "" ? "Nothing left to add." : `Nothing matches “${q.trim()}”.`}
							</ComboboxEmpty>
						)}
						{choices.error !== null && (
							<div role="alert" className="flex items-center justify-between gap-2 px-2.5 py-1.5 text-sm">
								<span className="text-bad-fg">{getErrorMessage(choices.error, choices.errorFallback)}</span>
								<Button type="button" variant="outline" size="sm" disabled={loading} onClick={choices.retry}>
									Retry
								</Button>
							</div>
						)}
						{loading && (
							<div className="px-2.5 py-1.5 text-sm text-fg3">
								{choices.workspacesLoading ? "Loading workspaces…" : "Loading resources…"}
							</div>
						)}
						{more > 0 && (
							<div className="px-2.5 py-1.5 text-[11.5px] text-fg3">{more.toString()} more, keep typing</div>
						)}
					</ComboboxList>
				</ComboboxContent>
			</div>
		</Combobox>
	);
}
