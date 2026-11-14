import { CheckIcon, ListFilterIcon } from "lucide-react";

import { CommandGroup, CommandItem } from "@/components/design/Command";
import { FilterPopover } from "@/components/design/FilterPopover";
import { auditLabel } from "@/pages/settings/auditEvents";

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
