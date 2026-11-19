import {
	ArrowUpRightIcon,
	BellIcon,
	CheckIcon,
	ChevronsUpDownIcon,
	CopyIcon,
	EllipsisIcon,
	FileCodeIcon,
	GlobeIcon,
	GlobeLockIcon,
	RotateCwIcon,
	Settings2Icon,
	Trash2Icon,
	Undo2Icon,
} from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus, type EnvironmentStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

import { ToggleChip } from "@/components/design/ToggleChip";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@/components/design/Command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
import { PartialBadge } from "@/components/design/PartialBadge";
import { ResourceStatusBadge, resourceStatusStyle } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { deploymentActor } from "@/lib/deployment-utils";
import { agoLabel, readyMs } from "@/lib/time";

import { NEEDS_ADMIN, NEEDS_WRITE } from "@/hooks/useMyScopes";
import { useWidth } from "@/hooks/useWidth";

import { depImage, depTag, isDeployed, type RegionView } from "./model";

interface SwitcherItem {
	id: string;
	name: string;
	description: string;
	status: ResourceStatus | undefined;
	href: string;
}

const SWITCHER_FILTER_FROM = 8;

function Switcher({ items, currentId, offset }: { items: SwitcherItem[]; currentId: string; offset: number }) {
	const [open, setOpen] = useState(false);
	const navigate = useNavigate();
	const sorted = items.toSorted((a, b) => a.name.localeCompare(b.name));
	return (
		<Popover open={open} onOpenChange={setOpen}>
			<PopoverTrigger
				render={<Button variant="ghost" size="icon-sm" aria-label="Switch resource" className="text-fg3" />}
			>
				<ChevronsUpDownIcon />
			</PopoverTrigger>
			<PopoverContent align="start" alignOffset={-offset} className="w-[280px] p-0" aria-label="Resources">
				<Command>
					{sorted.length > SWITCHER_FILTER_FROM && (
						<CommandInput compact placeholder="Filter resources" aria-label="Filter resources" />
					)}
					<CommandList className="max-h-80 p-1">
						<CommandEmpty>No resources match.</CommandEmpty>
						{sorted.map((it) => (
							<CommandItem
								key={it.id}
								value={`${it.name} ${it.description}`}
								className="h-auto min-h-9 py-1.5"
								onSelect={() => {
									setOpen(false);
									void navigate(it.href);
								}}
							>
								<StatusGlyph glyph={resourceStatusStyle(it.status).glyph} />
								<span className="flex min-w-0 flex-1 flex-col gap-px">
									<span className="truncate">{it.name}</span>
									{it.description !== "" && <span className="truncate text-[12px] text-fg3">{it.description}</span>}
								</span>
								{it.id === currentId && <CheckIcon aria-label="Current resource" className="size-3.5 text-fg3" />}
							</CommandItem>
						))}
					</CommandList>
				</Command>
			</PopoverContent>
		</Popover>
	);
}

function MenuIcon({ children }: { children: React.ReactNode }) {
	return <span className="flex text-fg3 [&_svg]:size-[15px]">{children}</span>;
}

export function ResourceHeader({
	resource,
	environment,
	regions,
	switcher,
	warnings,
	isSettings,
	redeploying,
	restarting,
	onRestart,
	canWrite,
	canDelete,
	rollbackTarget,
	onEvents,
	onSettings,
	onRedeploy,
	onRollback,
	onScale,
	onViewSpec,
	onCopyImage,
	onDelete,
	nowMs,
}: {
	resource: Resource;
	environment: EnvironmentStatus | undefined;
	regions: RegionView[];
	switcher: SwitcherItem[];
	warnings: number;
	isSettings: boolean;
	redeploying: boolean;
	restarting: boolean;
	onRestart: () => void;
	canWrite: boolean;
	canDelete: boolean;
	rollbackTarget: Deployment | undefined;
	onEvents: () => void;
	onSettings: () => void;
	onRedeploy: () => void;
	onRollback: () => void;
	onScale: () => void;
	onViewSpec: () => void;
	onCopyImage: () => void;
	onDelete: () => void;
	nowMs: number;
}) {
	const [titleWidth, measureTitle] = useWidth();
	const deployed = regions.filter(isDeployed);
	const primary = deployed[0]?.current;
	const image = depImage(primary);
	const since = primary === undefined ? undefined : readyMs(primary);
	const domain = resource.domains.find((d) => d.isPrimary) ?? resource.domains[0];
	const canRedeploy = canWrite && deployed.length > 0 && !redeploying;
	const writeHint = canWrite ? undefined : NEEDS_WRITE;

	return (
		<div className="flex flex-wrap items-start justify-between gap-4">
			<div className="flex min-w-0 grow basis-64 flex-col gap-2">
				<div className="flex flex-wrap items-center gap-3">
					<div className="flex items-center gap-1">
						<h1 ref={measureTitle} className="m-0 text-[28px] leading-[1.15] font-normal tracking-[-0.03em] break-all">
							{resource.displayName}
						</h1>
						<Switcher items={switcher} currentId={resource.id} offset={titleWidth + 4} />
					</div>
					<ResourceStatusBadge status={environment?.status} />
					{resource.partial !== undefined && <PartialBadge />}
				</div>
				<div className="flex min-w-0 flex-wrap items-center gap-x-[18px] gap-y-1 text-[12.5px] text-fg3">
					<span aria-label={`Name ${resource.name}`} className="font-mono text-[12px] text-fg2">
						{resource.name}
					</span>
					{since !== undefined && primary !== undefined && (
						<span>
							deployed {agoLabel(since, nowMs)} by {deploymentActor(primary)}
						</span>
					)}
					{domain !== undefined ? (
						<a
							href={`https://${domain.domain}`}
							target="_blank"
							rel="noopener noreferrer"
							aria-label={`Public at ${domain.domain}`}
							className="flex items-center gap-1.5 font-mono text-[12px] text-fg2 hover:text-foreground"
						>
							<GlobeIcon aria-hidden className="size-[13px]" />
							{domain.domain}
							<ArrowUpRightIcon aria-hidden className="size-3" />
						</a>
					) : (
						<Tooltip>
							<TooltipTrigger
								render={<span aria-label="Not reachable from the internet" />}
								tabIndex={0}
								className="flex items-center gap-1.5 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
							>
								<GlobeLockIcon aria-hidden className="size-[13px]" />
								internal only
							</TooltipTrigger>
							<TooltipContent>Not reachable from the internet</TooltipContent>
						</Tooltip>
					)}
					{deployed.length > 0 && (
						<span className="font-mono text-[12px]">{deployed.map((r) => r.name).join(", ")}</span>
					)}
				</div>
			</div>
			<div className="flex shrink-0 flex-wrap items-center gap-2">
				<Button variant="outline" size="sm" aria-haspopup="dialog" onClick={onEvents}>
					<BellIcon />
					Events
					{warnings > 0 && (
						<span
							aria-label={`${warnings.toString()} warnings in the last hour`}
							className="ml-0.5 inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-warn-bg px-[5px] font-mono text-[11px] text-warn-fg"
						>
							{warnings}
						</span>
					)}
				</Button>
				<ToggleChip
					variant="button"
					className="h-[30px] px-2.5 text-[12.5px]"
					pressed={isSettings}
					onPressedChange={onSettings}
				>
					<Settings2Icon />
					Settings
				</ToggleChip>
				<Button variant="outline" size="sm" disabled={!canRedeploy} title={writeHint} onClick={onRedeploy}>
					<RotateCwIcon />
					{redeploying ? "Redeploying…" : "Redeploy"}
				</Button>
				<DropdownMenu>
					<DropdownMenuTrigger render={<Button variant="outline" size="icon" aria-label="More actions" />}>
						<EllipsisIcon />
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="w-auto min-w-[220px]">
						<DropdownMenuItem
							disabled={!canWrite || rollbackTarget === undefined}
							title={writeHint}
							onClick={onRollback}
						>
							<MenuIcon>
								<Undo2Icon />
							</MenuIcon>
							{rollbackTarget === undefined ? "Roll back" : `Roll back to ${depTag(rollbackTarget)}`}
						</DropdownMenuItem>
						<DropdownMenuItem disabled={!canRedeploy || restarting} title={writeHint} onClick={onRestart}>
							<MenuIcon>
								<RotateCwIcon />
							</MenuIcon>
							Restart all replicas
						</DropdownMenuItem>
						<DropdownMenuItem onClick={onScale}>
							<MenuIcon>
								<ChevronsUpDownIcon />
							</MenuIcon>
							Scale
						</DropdownMenuItem>
						<DropdownMenuItem disabled={primary === undefined} onClick={onViewSpec}>
							<MenuIcon>
								<FileCodeIcon />
							</MenuIcon>
							View spec
						</DropdownMenuItem>
						<DropdownMenuItem disabled={image === ""} onClick={onCopyImage}>
							<MenuIcon>
								<CopyIcon />
							</MenuIcon>
							Copy image reference
						</DropdownMenuItem>
						<DropdownMenuSeparator />
						<DropdownMenuItem
							variant="destructive"
							disabled={!canDelete}
							title={canDelete ? undefined : NEEDS_ADMIN}
							onClick={onDelete}
						>
							<MenuIcon>
								<Trash2Icon />
							</MenuIcon>
							Delete resource
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
		</div>
	);
}
