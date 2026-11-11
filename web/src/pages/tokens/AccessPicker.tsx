import { SearchIcon, XIcon } from "lucide-react";
import { useRef, useState } from "react";

import {
	Combobox,
	ComboboxChipsInput,
	ComboboxContent,
	ComboboxEmpty,
	ComboboxItem,
	ComboboxList,
} from "@/components/design/Combobox";
import { Button } from "@/components/design/Button";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { LEVEL_LABEL } from "@/hooks/useMyScopes";

import { ALLOWS, heldLevel, LEVELS } from "./model";
import type { EntityNode, EntityTree, HeldScopes, Level } from "./model";
import { kindIcon } from "./icons";

export interface AccessItem {
	key: string;
	level: Level;
}

const KIND_RANK = { user: 0, org: 1, ws: 2, res: 3, system: 4 } as const;
const MAX_RESULTS = 40;

export function AccessPicker({
	tree,
	held,
	items,
	allowUser,
	resourcesLoading,
	resourcesError,
	retryResources,
	invalid,
	onChange,
}: {
	tree: EntityTree;
	held: HeldScopes;
	items: AccessItem[];
	allowUser: boolean;
	resourcesLoading: boolean;
	resourcesError: unknown;
	retryResources: () => void;
	invalid: boolean;
	onChange: (items: AccessItem[]) => void;
}) {
	const [q, setQ] = useState("");
	const [focus, setFocus] = useState(false);
	const highlightedRef = useRef<EntityNode | undefined>(undefined);
	const anchorRef = useRef<HTMLDivElement>(null);
	const levelByKey = new Map(items.map((x) => [x.key, x.level]));
	const query = q.trim().toLowerCase();

	const coveredBy = (node: EntityNode, level: Level): string | null => {
		let p = node.parent;
		while (p !== null) {
			const parentKey = p;
			const itLevel = levelByKey.get(parentKey);
			const parent = tree.nodes.get(parentKey);
			if (itLevel !== undefined && itLevel >= level && parent !== undefined) return parent.label;
			p = parent?.parent ?? null;
		}
		return null;
	};

	const pool = tree.order
		.filter((n) => n.kind !== "system" && (allowUser || n.kind !== "user"))
		.filter((n) => !levelByKey.has(n.key) && (query === "" || n.label.toLowerCase().includes(query)))
		.toSorted((a, b) => {
			if (query !== "") {
				const sa = a.label.toLowerCase().startsWith(query) ? 0 : 1;
				const sb = b.label.toLowerCase().startsWith(query) ? 0 : 1;
				if (sa !== sb) return sa - sb;
			}
			return KIND_RANK[a.kind] - KIND_RANK[b.kind];
		});
	const shown = pool.slice(0, MAX_RESULTS);

	const add = (node: EntityNode) => {
		const lvl = heldLevel(held, tree, node.key);
		if (lvl === 0) return;
		onChange([...items, { key: node.key, level: 1 }]);
		setQ("");
	};

	const resultNote = (n: EntityNode): string => {
		switch (n.kind) {
			case "user":
				return "Your user account";
			case "org":
				return "Organization";
			case "ws":
				if (resourcesLoading || resourcesError !== null) return "Workspace";
				return `Workspace · ${(tree.childCount.get(n.key) ?? 0).toString()} resources`;
			case "res": {
				const cov = coveredBy(n, 1);
				if (cov !== null) return `Included in ${cov}`;
				const parentKey = n.parent;
				return parentKey === null ? "Resource" : (tree.nodes.get(parentKey)?.label ?? "Resource");
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
			case "ws":
				if (resourcesLoading || resourcesError !== null) return "Workspace · all resources";
				return `Workspace · all ${(tree.childCount.get(n.key) ?? 0).toString()} resources`;
			case "res": {
				const parentKey = n.parent;
				return parentKey === null ? "Resource" : (tree.nodes.get(parentKey)?.label ?? "Resource");
			}
			case "system":
				return "System";
		}
	};

	const rows = items.map((it, i) => {
		const n = tree.nodes.get(it.key);
		if (n === undefined) return null;
		const lvl = heldLevel(held, tree, n.key);
		return (
			<div
				key={it.key}
				className="grid grid-cols-[22px_minmax(0,1fr)_auto_22px] items-center gap-2.5 border-b border-line py-2 pr-2.5 pl-3"
			>
				<span className="flex text-fg3 [&_svg]:size-3.5">{kindIcon(n.kind)}</span>
				<span className="flex min-w-0 flex-col gap-px">
					<span className="truncate font-mono text-[12.5px]">{n.label}</span>
					<span className="truncate text-[11.5px] text-fg3">{itemKind(n)}</span>
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
			<div
				ref={anchorRef}
				className={cn(
					"relative rounded-lg border bg-raise",
					focus ? "border-fg4" : invalid ? "border-bad-fg" : "border-line2",
				)}
			>
				{items.length === 0 && (
					<div className="border-b border-line p-3 text-[12.5px] text-fg3">No permissions yet.</div>
				)}
				{rows}
				<div className="flex h-[38px] items-center gap-2 px-3">
					<SearchIcon className="size-3.5 text-fg3" />
					<ComboboxChipsInput
						onFocus={() => {
							setFocus(true);
						}}
						onKeyDown={(e) => {
							if (e.key !== "Enter" || highlightedRef.current !== undefined) return;
							e.preventDefault();
							e.preventBaseUIHandler();
							const first = shown.find((n) => heldLevel(held, tree, n.key) > 0);
							if (first !== undefined) add(first);
						}}
						aria-invalid={invalid}
						placeholder="Add a workspace or resource"
						autoComplete="off"
						aria-label="Add a workspace or resource"
						className="min-w-0 flex-1 border-0 bg-transparent text-[13px] text-foreground outline-none placeholder:text-fg4"
					/>
				</div>
				<ComboboxContent anchor={anchorRef}>
					<ComboboxList>
						{shown.map((n) => {
							const lvl = heldLevel(held, tree, n.key);
							const disabled = lvl === 0;
							const covered = n.kind === "res" && coveredBy(n, 1) !== null;
							return (
								<ComboboxItem
									key={n.key}
									value={n}
									disabled={disabled}
									title={disabled ? `You have no access to ${n.label}` : undefined}
									className={cn(disabled && "opacity-45", covered && "opacity-50")}
								>
									<span className="flex text-fg3 [&_svg]:size-3.5">{kindIcon(n.kind)}</span>
									<span className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{n.label}</span>
									<span className="text-[11.5px] whitespace-nowrap text-fg3">
										{disabled ? "No access" : resultNote(n)}
									</span>
								</ComboboxItem>
							);
						})}
						{!resourcesLoading && resourcesError === null && (
							<ComboboxEmpty className="p-3 text-[12.5px] text-fg3">
								{query === "" ? "Nothing left to add." : `Nothing matches “${q.trim()}”.`}
							</ComboboxEmpty>
						)}
						{resourcesError !== null && (
							<div role="alert" className="flex items-center justify-between gap-2 px-2.5 py-1.5 text-sm">
								<span className="text-bad-fg">{getErrorMessage(resourcesError, "Failed to load resources")}</span>
								<Button type="button" variant="outline" size="sm" disabled={resourcesLoading} onClick={retryResources}>
									Retry
								</Button>
							</div>
						)}
						{resourcesLoading && <div className="px-2.5 py-1.5 text-sm text-fg3">Loading resources…</div>}
						{pool.length > shown.length && (
							<div className="px-2.5 py-1.5 text-[11.5px] text-fg3">
								{(pool.length - shown.length).toString()} more, keep typing
							</div>
						)}
					</ComboboxList>
				</ComboboxContent>
			</div>
		</Combobox>
	);
}
