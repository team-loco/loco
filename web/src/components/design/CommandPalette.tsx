import { CornerDownLeftIcon, SearchIcon } from "lucide-react";
import { useId, useState, type KeyboardEvent, type ReactNode } from "react";

import { Dialog, DialogContent, DialogTitle } from "@/components/design/Dialog";
import { Kbd } from "@/components/design/Kbd";
import { filterPalette, type PaletteEntry } from "@/lib/command-palette";
import { cn } from "@/lib/utils";

export interface PaletteItem extends PaletteEntry {
	icon: ReactNode;
	meta?: string | undefined;
	run: () => void;
}

const PLACEHOLDER = "Search resources, pages and actions";

function PaletteBody({ items, onClose }: { items: readonly PaletteItem[]; onClose: () => void }) {
	const listId = useId();
	const [query, setQuery] = useState("");
	const [cursor, setCursor] = useState(0);
	const groups = filterPalette(items, query);
	const flat = groups.flatMap((g) => g.items);
	const active = Math.min(cursor, Math.max(0, flat.length - 1));
	const optionId = (id: string) => `${listId}-${id}`;
	const activeItem = flat[active];

	const select = (item: PaletteItem) => {
		onClose();
		item.run();
	};

	const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		switch (event.key) {
			case "ArrowDown":
				event.preventDefault();
				setCursor(Math.min(flat.length - 1, active + 1));
				return;
			case "ArrowUp":
				event.preventDefault();
				setCursor(Math.max(0, active - 1));
				return;
			case "Home":
				event.preventDefault();
				setCursor(0);
				return;
			case "End":
				event.preventDefault();
				setCursor(flat.length - 1);
				return;
			case "Enter":
				event.preventDefault();
				if (activeItem !== undefined) select(activeItem);
				return;
		}
	};

	return (
		<>
			<div className="flex h-[52px] shrink-0 items-center gap-2.5 border-b border-line pr-3.5 pl-4">
				<SearchIcon aria-hidden className="size-4 shrink-0 text-fg3" />
				<input
					autoFocus
					value={query}
					onChange={(event) => {
						setQuery(event.target.value);
						setCursor(0);
					}}
					onKeyDown={onKeyDown}
					placeholder={PLACEHOLDER}
					aria-label={PLACEHOLDER}
					role="combobox"
					aria-expanded
					aria-controls={listId}
					aria-autocomplete="list"
					aria-activedescendant={activeItem === undefined ? undefined : optionId(activeItem.id)}
					autoComplete="off"
					spellCheck={false}
					className="min-w-0 flex-1 bg-transparent text-[15px] text-foreground outline-none placeholder:text-fg4"
				/>
				<Kbd>esc</Kbd>
			</div>
			<div id={listId} role="listbox" aria-label="Results" className="max-h-[min(420px,60vh)] overflow-y-auto p-1.5">
				{groups.map((g) => (
					<div key={g.group} role="group" aria-labelledby={`${listId}-${g.group}`}>
						<div id={`${listId}-${g.group}`} className="px-2.5 pt-2 pb-1 text-[11.5px] text-fg3">
							{g.group}
						</div>
						{g.items.map((item) => {
							const on = item === activeItem;
							const hasMeta = item.meta !== undefined && item.meta !== "";
							return (
								<button
									type="button"
									key={item.id}
									id={optionId(item.id)}
									role="option"
									aria-selected={on}
									tabIndex={-1}
									ref={on ? (el) => el?.scrollIntoView({ block: "nearest" }) : undefined}
									onClick={() => {
										select(item);
									}}
									onMouseMove={() => {
										if (!on) setCursor(flat.indexOf(item));
									}}
									className={cn(
										"flex h-9 w-full items-center gap-2.5 rounded-[7px] px-2.5 text-left text-[13.5px] text-foreground outline-none",
										on && "bg-panel dark:bg-panel2",
									)}
								>
									<span aria-hidden className="flex w-4 justify-center text-fg3 [&_svg]:size-[15px]">
										{item.icon}
									</span>
									<span className="truncate">{item.label}</span>
									{hasMeta && <span className="ml-auto min-w-0 truncate text-xs text-fg3">{item.meta}</span>}
									<CornerDownLeftIcon
										aria-hidden
										className={cn("size-3 shrink-0 text-fg4", !hasMeta && "ml-auto", !on && "opacity-0")}
									/>
								</button>
							);
						})}
					</div>
				))}
				{flat.length === 0 && <div className="px-3 py-7 text-center text-[13px] text-fg3">Nothing matches.</div>}
			</div>
			<div className="flex shrink-0 items-center gap-4 border-t border-line px-4 py-2 text-xs text-fg3">
				<span className="flex items-center gap-1.5">
					<Kbd>↑</Kbd>
					<Kbd>↓</Kbd>
					to navigate
				</span>
				<span className="flex items-center gap-1.5">
					<Kbd>↵</Kbd>
					to open
				</span>
				<span className="flex items-center gap-1.5">
					<Kbd>esc</Kbd>
					to close
				</span>
			</div>
		</>
	);
}

export function CommandPalette({
	open,
	onOpenChange,
	items,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	items: readonly PaletteItem[];
}) {
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				className="top-[14vh] w-[620px] translate-y-0 overflow-hidden border-line2 dark:bg-panel"
			>
				<DialogTitle className="sr-only">Command palette</DialogTitle>
				<PaletteBody
					items={items}
					onClose={() => {
						onOpenChange(false);
					}}
				/>
			</DialogContent>
		</Dialog>
	);
}
