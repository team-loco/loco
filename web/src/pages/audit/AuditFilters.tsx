import { useCallback, useRef, useState } from "react";
import { CheckIcon, ListFilterIcon } from "lucide-react";

import { CommandGroup, CommandItem } from "@/components/design/Command";
import { FilterPopover } from "@/components/design/FilterPopover";
import { SearchInput } from "@/components/design/SearchInput";
import { auditLabel } from "@/pages/settings/auditEvents";

import { CATEGORIES, MIN_QUERY_CHARS, toggleCategory, toggleType, typesLabel } from "./model";

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

const SEARCH_DEBOUNCE_MS = 350;

export function AuditSearch({ value, onChange }: { value: string; onChange: (value: string) => void }) {
	const [input, setInput] = useState({ source: value, draft: value });
	if (input.source !== value) setInput({ source: value, draft: value });
	const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const ref = useCallback(
		() => () => {
			clearTimeout(timerRef.current);
		},
		[],
	);
	return (
		<SearchInput
			ref={ref}
			aria-label="Search events"
			placeholder="Search request ID, actor or target"
			maxLength={256}
			hint={Array.from(input.draft).length < MIN_QUERY_CHARS ? `${MIN_QUERY_CHARS}+ characters` : undefined}
			value={input.draft}
			onChange={(event) => {
				const control = event.currentTarget;
				const draft = control.value;
				setInput({ source: value, draft });
				clearTimeout(timerRef.current);
				timerRef.current = setTimeout(() => {
					if (control.value === draft) onChange(draft);
				}, SEARCH_DEBOUNCE_MS);
			}}
			onClear={() => {
				clearTimeout(timerRef.current);
				setInput({ source: value, draft: "" });
				onChange("");
			}}
		/>
	);
}
