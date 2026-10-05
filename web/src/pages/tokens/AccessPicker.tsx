import { SearchIcon, XIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { cn } from "@/lib/utils";

import { ALLOWS, heldLevel, LEVEL_LABEL, LEVELS } from "./model";
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
	invalid,
	onChange,
}: {
	tree: EntityTree;
	held: HeldScopes;
	items: AccessItem[];
	allowUser: boolean;
	resourcesLoading: boolean;
	invalid: boolean;
	onChange: (items: AccessItem[]) => void;
}) {
	const [q, setQ] = useState("");
	const [focus, setFocus] = useState(false);
	const have = new Set(items.map((x) => x.key));
	const query = q.trim().toLowerCase();

	const coveredBy = (node: EntityNode, level: Level): string | null => {
		let p = node.parent;
		while (p !== null) {
			const parentKey = p;
			const it = items.find((x) => x.key === parentKey);
			const parent = tree.nodes.get(parentKey);
			if (it !== undefined && it.level >= level && parent !== undefined) return parent.label;
			p = parent?.parent ?? null;
		}
		return null;
	};

	const pool = tree.order
		.filter((n) => n.kind !== "system" && (allowUser || n.kind !== "user"))
		.filter((n) => !have.has(n.key) && (query === "" || n.label.toLowerCase().includes(query)))
		.sort((a, b) => {
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
				return `Workspace · all ${(tree.childCount.get(n.key) ?? 0).toString()} resources`;
			case "res": {
				const parentKey = n.parent;
				return parentKey === null ? "Resource" : (tree.nodes.get(parentKey)?.label ?? "Resource");
			}
			case "system":
				return "System";
		}
	};

	return (
		<div
			className={cn(
				"flex flex-col overflow-hidden rounded-lg border bg-background",
				focus ? "border-fg4" : invalid ? "border-bad-fg" : "border-line",
			)}
		>
			<div className="flex h-[38px] items-center gap-2 px-2.5">
				<SearchIcon className="size-3.5 text-fg3" />
				<input
					value={q}
					onChange={(e) => {
						setQ(e.target.value);
						setFocus(true);
					}}
					onFocus={() => {
						setFocus(true);
					}}
					onClick={() => {
						setFocus(true);
					}}
					onBlur={() => {
						setFocus(false);
					}}
					onKeyDown={(e) => {
						if (e.key === "Escape" && focus) {
							e.stopPropagation();
							e.preventDefault();
							setFocus(false);
						} else if (e.key === "Enter") {
							e.preventDefault();
							const first = shown.find((n) => heldLevel(held, tree, n.key) > 0);
							if (first !== undefined) add(first);
						}
					}}
					placeholder="Add a workspace or resource"
					autoComplete="off"
					aria-label="Add a workspace or resource"
					className="min-w-0 flex-1 border-0 bg-transparent text-[13.5px] text-foreground outline-none placeholder:text-fg4"
				/>
			</div>
			{focus && (
				<div
					onMouseDown={(e) => {
						e.preventDefault();
					}}
					className="max-h-[228px] overflow-y-auto border-t border-line p-1"
				>
					{shown.map((n) => {
						const lvl = heldLevel(held, tree, n.key);
						const disabled = lvl === 0;
						const covered = n.kind === "res" && coveredBy(n, 1) !== null;
						return (
							<button
								key={n.key}
								type="button"
								disabled={disabled}
								title={disabled ? `You have no access to ${n.label}` : undefined}
								onClick={() => {
									add(n);
								}}
								className={cn(
									"flex h-[34px] w-full items-center gap-2.5 rounded-sm px-2 text-left text-foreground hover:bg-bg3",
									disabled ? "cursor-not-allowed opacity-45" : "cursor-pointer",
									covered && "opacity-50",
								)}
							>
								<span className="flex text-fg3 [&_svg]:size-3.5">{kindIcon(n.kind)}</span>
								<span className="min-w-0 flex-1 truncate font-medium">{n.label}</span>
								<span className="text-sm whitespace-nowrap text-fg3">{disabled ? "No access" : resultNote(n)}</span>
							</button>
						);
					})}
					{shown.length === 0 && !resourcesLoading && <div className="px-2.5 py-2 text-fg3">No matches</div>}
					{resourcesLoading && <div className="px-2.5 py-1.5 text-sm text-fg3">Loading resources…</div>}
					{pool.length > shown.length && (
						<div className="px-2.5 py-1.5 text-sm text-fg3">
							{(pool.length - shown.length).toString()} more. Keep typing to narrow.
						</div>
					)}
				</div>
			)}
			{items.map((it, i) => {
				const n = tree.nodes.get(it.key);
				if (n === undefined) return null;
				const lvl = heldLevel(held, tree, n.key);
				return (
					<div key={it.key} className="flex min-h-11 items-center gap-2.5 border-t border-line pr-1.5 pl-2.5">
						<span className="flex text-fg3 [&_svg]:size-3.5">{kindIcon(n.kind)}</span>
						<span className="flex min-w-0 flex-1 flex-col">
							<span className="truncate font-medium">{n.label}</span>
							<span className="text-sm text-fg3">{itemKind(n)}</span>
						</span>
						<ToggleGroup
							variant="segmented"
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
									<ToggleGroupItem
										key={l}
										value={l.toString()}
										disabled={dis}
										title={tip}
										className="h-[26px]! text-[12.5px] disabled:pointer-events-auto disabled:cursor-not-allowed disabled:opacity-45"
									>
										{LEVEL_LABEL[l]}
									</ToggleGroupItem>
								);
							})}
						</ToggleGroup>
						<Button
							type="button"
							variant="ghost"
							size="icon-sm"
							title="Remove"
							className="text-fg3 hover:text-foreground"
							onClick={() => {
								onChange(items.filter((_, j) => j !== i));
							}}
						>
							<XIcon className="size-4" />
						</Button>
					</div>
				);
			})}
		</div>
	);
}
