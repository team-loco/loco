import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { CheckIcon, LayersIcon } from "lucide-react";

import { CommandItem } from "@/components/design/Command";
import { FilterPopover } from "@/components/design/FilterPopover";

export function WorkspaceFilter({
	workspaces,
	counts,
	value,
	clearable = true,
	onChange,
}: {
	workspaces: Workspace[];
	counts?: ReadonlyMap<string, number> | undefined;
	value: string;
	clearable?: boolean;
	onChange: (workspaceId: string) => void;
}) {
	return (
		<FilterPopover
			icon={<LayersIcon />}
			label="Workspace"
			value={workspaces.find((w) => w.id === value)?.name ?? ""}
			mono
			onClear={
				clearable
					? () => {
							onChange("");
						}
					: undefined
			}
			placeholder="Find a workspace"
		>
			{(close) =>
				workspaces.map((w) => (
					<CommandItem
						key={w.id}
						value={w.name}
						onSelect={() => {
							onChange(w.id);
							close();
						}}
					>
						<span className="min-w-0 flex-1 truncate font-mono">{w.name}</span>
						{counts !== undefined && (
							<span className="font-mono text-[11.5px] text-fg3">{(counts.get(w.id) ?? 0).toLocaleString()}</span>
						)}
						{w.id === value && <CheckIcon className="size-3.5" />}
					</CommandItem>
				))
			}
		</FilterPopover>
	);
}
