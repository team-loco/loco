import { CheckIcon, ListFilterIcon, UserIcon } from "lucide-react";

import { CommandGroup, CommandItem } from "@/components/design/Command";
import { FilterPopover } from "@/components/design/FilterPopover";
import { AUDIT_EVENT_TYPES, auditLabel } from "@/pages/settings/auditEvents";

import { CATEGORIES, toggleCategory, toggleType, typesLabel } from "./model";

export function EventFilter({ types, onChange }: { types: string[]; onChange: (types: string[]) => void }) {
	return (
		<FilterPopover
			icon={<ListFilterIcon />}
			label="Event"
			value={typesLabel(types)}
			onClear={() => {
				onChange([]);
			}}
			placeholder="Find an event type"
			footer={
				<>
					<span>
						{types.length} of {AUDIT_EVENT_TYPES.length}
					</span>
					{types.length > 0 && (
						<button
							type="button"
							className="text-fg2 hover:text-foreground"
							onClick={() => {
								onChange([]);
							}}
						>
							Clear {types.length}
						</button>
					)}
				</>
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

export function ActorFilter({
	items,
	value,
	onChange,
}: {
	items: { id: string; name: string; detail: string; group: string }[];
	value: string;
	onChange: (id: string) => void;
}) {
	return (
		<FilterPopover
			icon={<UserIcon />}
			label="Actor"
			value={items.find((i) => i.id === value)?.name ?? ""}
			onClear={() => {
				onChange("");
			}}
			placeholder="Find a person or token"
		>
			{(close) =>
				["People", "Tokens", "Loco"].map((group) => (
					<CommandGroup key={group} heading={group}>
						{items
							.filter((i) => i.group === group)
							.map((item) => (
								<CommandItem
									key={item.id}
									value={`${item.name} ${item.detail}`}
									onSelect={() => {
										onChange(item.id);
										close();
									}}
								>
									<span className="flex min-w-0 flex-1 flex-col">
										<span className="truncate">{item.name}</span>
										{item.detail !== "" && <span className="truncate text-[11.5px] text-fg3">{item.detail}</span>}
									</span>
									{item.id === value && <CheckIcon className="size-3.5" />}
								</CommandItem>
							))}
					</CommandGroup>
				))
			}
		</FilterPopover>
	);
}
