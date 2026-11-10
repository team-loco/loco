import {
	ArrowUpRightIcon,
	BellIcon,
	CheckIcon,
	ChevronsUpDownIcon,
	CopyIcon,
	EllipsisIcon,
	FileCodeIcon,
	RotateCwIcon,
	Settings2Icon,
	SlidersHorizontalIcon,
	Trash2Icon,
	Undo2Icon,
} from "lucide-react";
import { Link } from "react-router";
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
import { PartialBadge } from "@/components/design/PartialBadge";
import { SoonTag } from "@/components/design/SoonTag";
import { ResourceStatusBadge, resourceStatusStyle } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { shortImageRef } from "@/lib/image";
import { agoLabel } from "@/lib/time";

import { NEEDS_ADMIN, NEEDS_WRITE } from "@/hooks/useMyScopes";

import { depImage, depTag, isDeployed, startedMs, type RegionView } from "./model";

interface SwitcherItem {
	id: string;
	name: string;
	status: ResourceStatus | undefined;
	href: string;
}

function Switcher({ items, currentId }: { items: SwitcherItem[]; currentId: string }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="ghost" size="icon-sm" aria-label="Switch resource" className="text-fg3" />}
			>
				<ChevronsUpDownIcon />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="max-h-80 w-auto min-w-[240px]">
				{items.map((it) => (
					<DropdownMenuItem key={it.id} render={<Link to={it.href} />} className="gap-2">
						<StatusGlyph glyph={resourceStatusStyle(it.status).glyph} />
						<span className="flex-1 truncate">{it.name}</span>
						{it.id === currentId && <CheckIcon className="size-3.5 text-fg3" />}
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
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
	dirty,
	redeploying,
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
	dirty: boolean;
	redeploying: boolean;
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
	const deployed = regions.filter(isDeployed);
	const primary = deployed[0]?.current;
	const image = depImage(primary);
	const since = primary === undefined ? undefined : startedMs(primary);
	const domain = resource.domains.find((d) => d.isPrimary) ?? resource.domains[0];
	const canRedeploy = canWrite && deployed.length > 0 && !redeploying;
	const writeHint = canWrite ? undefined : NEEDS_WRITE;

	return (
		<div className="flex flex-wrap items-end justify-between gap-4">
			<div className="flex min-w-0 flex-col gap-2">
				<div className="flex flex-wrap items-center gap-3">
					<div className="flex items-center gap-1">
						<h1 className="m-0 text-[28px] leading-[1.15] font-normal tracking-[-0.03em] break-all">{resource.name}</h1>
						<Switcher items={switcher} currentId={resource.id} />
					</div>
					<ResourceStatusBadge status={environment?.status} />
					{resource.partial !== undefined && <PartialBadge />}
				</div>
				<div className="flex flex-wrap items-center gap-x-[18px] gap-y-1 text-[12.5px] text-fg3">
					{image !== "" && (
						<span className="font-mono text-[12px] text-fg2" title={image}>
							{shortImageRef(image)}
						</span>
					)}
					{since !== undefined && <span>deployed {agoLabel(since, nowMs)}</span>}
					{domain !== undefined ? (
						<a
							href={`https://${domain.domain}`}
							target="_blank"
							rel="noopener noreferrer"
							className="flex items-center gap-1 font-mono text-[12px] text-fg2 hover:text-foreground"
						>
							{domain.domain}
							<ArrowUpRightIcon aria-hidden className="size-3" />
						</a>
					) : (
						<span>internal only</span>
					)}
					{deployed.length > 0 && (
						<span className="font-mono text-[12px]">{deployed.map((r) => r.name).join(", ")}</span>
					)}
				</div>
			</div>
			<div className="flex flex-wrap items-center gap-2">
				<Button variant="outline" aria-haspopup="dialog" onClick={onEvents}>
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
				<ToggleChip variant="button" pressed={isSettings} onPressedChange={onSettings}>
					<Settings2Icon />
					Settings
					{dirty && <span aria-label="unsaved changes" className="size-1.5 rounded-full bg-warn" />}
				</ToggleChip>
				<Button variant="outline" disabled={!canRedeploy} title={writeHint} onClick={onRedeploy}>
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
						<DropdownMenuItem disabled>
							<MenuIcon>
								<RotateCwIcon />
							</MenuIcon>
							Restart all replicas
							<SoonTag />
						</DropdownMenuItem>
						<DropdownMenuItem onClick={onScale}>
							<MenuIcon>
								<SlidersHorizontalIcon />
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
