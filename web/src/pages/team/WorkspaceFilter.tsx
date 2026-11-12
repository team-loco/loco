import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { CheckIcon, ChevronDownIcon, LayersIcon, XIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@/components/design/Command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";

export function WorkspaceFilter({
	workspaces,
	counts,
	value,
	onChange,
}: {
	workspaces: Workspace[];
	counts: ReadonlyMap<string, number>;
	value: string;
	onChange: (workspaceId: string) => void;
}) {
	const [open, setOpen] = useState(false);
	const active = workspaces.find((w) => w.id === value);
	return (
		<span className="flex shrink-0 items-center">
			<Popover open={open} onOpenChange={setOpen}>
				<PopoverTrigger
					render={
						<Button
							variant="outline"
							className={active === undefined ? "px-2.5" : "rounded-r-none border-r-0 px-2.5"}
						/>
					}
				>
					<LayersIcon className="text-fg3" />
					{active === undefined ? "Workspace" : <span className="font-mono">{active.name}</span>}
					{active === undefined && <ChevronDownIcon className="size-3 text-fg3" />}
				</PopoverTrigger>
				<PopoverContent align="start" className="w-[280px] p-0" aria-label="Filter by workspace">
					<Command>
						<CommandInput placeholder="Find a workspace" className="font-mono" />
						<CommandList>
							<CommandEmpty>No workspace matches.</CommandEmpty>
							{workspaces.map((w) => (
								<CommandItem
									key={w.id}
									value={w.name}
									onSelect={() => {
										onChange(w.id);
										setOpen(false);
									}}
								>
									<span className="min-w-0 flex-1 truncate font-mono">{w.name}</span>
									<span className="font-mono text-[11.5px] text-fg3">{(counts.get(w.id) ?? 0).toLocaleString()}</span>
									{w.id === value && <CheckIcon className="size-3.5" />}
								</CommandItem>
							))}
						</CommandList>
					</Command>
				</PopoverContent>
			</Popover>
			{active !== undefined && (
				<Button
					variant="outline"
					size="icon"
					aria-label="Clear workspace filter"
					className="rounded-l-none"
					onClick={() => {
						onChange("");
					}}
				>
					<XIcon />
				</Button>
			)}
		</span>
	);
}
