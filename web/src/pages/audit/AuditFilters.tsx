import { CheckIcon, ListFilterIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { CommandGroup, CommandItem } from "@/components/design/Command";
import { FilterPopover } from "@/components/design/FilterPopover";
import { auditLabel } from "@/pages/settings/auditEvents";

import { CATEGORIES, toggleCategory, toggleType, typesLabel } from "./model";

export function EventFilter({ types, onChange }: { types: string[]; onChange: (types: string[]) => void }) {
	const clear = () => {
		onChange([]);
	};
	return (
		<FilterPopover
			icon={<ListFilterIcon />}
			label="Event"
			value={typesLabel(types)}
			onClear={clear}
			placeholder="Find an event type"
			footer={
				types.length > 0 && (
					<div className="flex items-center justify-between border-t border-line px-3 py-2 text-[12px] text-fg3">
						<span className="font-mono">{types.length} selected</span>
						<Button variant="link" onClick={clear}>
							Clear
						</Button>
					</div>
				)
			}
		>
			{() =>
				CATEGORIES.map((c) => (
					<CommandGroup key={c.value}>
						<CommandItem
							value={`${c.label} category`}
							className="h-7 text-[11.5px] text-fg3"
							onSelect={() => {
								onChange(toggleCategory(types, c.types));
							}}
						>
							<span className="flex-1">{c.label}</span>
							<span className="text-[11px]">{c.types.every((t) => types.includes(t)) ? "Clear" : "All"}</span>
						</CommandItem>
						{c.types.map((t) => (
							<CommandItem
								key={t}
								value={`${auditLabel(t)} ${t}`}
								className="h-auto py-1.5"
								onSelect={() => {
									onChange(toggleType(types, t));
								}}
							>
								<span className="flex min-w-0 flex-1 flex-col leading-[1.3]">
									<span className="truncate">{auditLabel(t)}</span>
									<span className="truncate font-mono text-[11px] text-fg3">{t}</span>
								</span>
								{types.includes(t) && <CheckIcon className="size-3.5" />}
							</CommandItem>
						))}
					</CommandGroup>
				))
			}
		</FilterPopover>
	);
}

/** A single-choice filter over named items, such as a workspace's resources or environments. */
export function PickFilter({
	icon,
	label,
	items,
	value,
	placeholder,
	onChange,
}: {
	icon: ReactNode;
	label: string;
	items: readonly { id: string; name: string }[];
	value: string;
	placeholder?: string;
	onChange: (id: string) => void;
}) {
	return (
		<FilterPopover
			icon={icon}
			label={label}
			value={items.find((i) => i.id === value)?.name ?? ""}
			mono
			onClear={() => {
				onChange("");
			}}
			placeholder={placeholder}
		>
			{(close) =>
				items.map((i) => (
					<CommandItem
						key={i.id}
						value={i.name}
						onSelect={() => {
							onChange(i.id === value ? "" : i.id);
							close();
						}}
					>
						<span className="min-w-0 flex-1 truncate font-mono">{i.name}</span>
						{i.id === value && <CheckIcon className="size-3.5" />}
					</CommandItem>
				))
			}
		</FilterPopover>
	);
}
