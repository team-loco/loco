import { CopyIcon, EllipsisIcon, Trash2Icon } from "lucide-react";
import type { ReactNode } from "react";
import type { Token } from "@gen/loco/token/v1/token_pb";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { LevelChip } from "@/components/design/LevelChip";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { LEVEL_LABEL, LEVEL_TEXT } from "@/hooks/useMyScopes";
import { maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { accessChips, expiresLabel, kindLabel, lastUsedLabel, relAgo, tokenStatus } from "./model";
import type { AccessChip, EntityTree } from "./model";

const SHOWN_CHIPS = 3;
const SKELETON_ROWS = [0, 1, 2, 3];
const COLUMNS = 6;

function AccessCell({ chips }: { chips: AccessChip[] }) {
	const rest = chips.length - SHOWN_CHIPS;
	return (
		<div className="flex min-w-0 flex-wrap gap-1">
			{chips.slice(0, SHOWN_CHIPS).map((c) => (
				<LevelChip key={c.key} level={c.level} name={c.node.label} />
			))}
			{rest > 0 && (
				<Tooltip>
					<TooltipTrigger
						render={<button type="button" aria-label={`Show all ${chips.length.toString()} grants`} />}
						className="inline-flex h-6 items-center rounded-md bg-panel2 px-2 font-mono text-[11.5px] text-fg2 hover:text-foreground"
					>
						+{rest}
					</TooltipTrigger>
					<TooltipContent className="flex max-w-[360px] flex-col gap-1 py-2">
						{chips.map((c) => (
							<span key={c.key} className="flex gap-2">
								<span className={cn("w-11 shrink-0 font-medium", LEVEL_TEXT[c.level])}>{LEVEL_LABEL[c.level]}</span>
								<span className="truncate">
									{c.node.label} <span className="text-fg3">{kindLabel(c.node.kind).toLowerCase()}</span>
								</span>
							</span>
						))}
					</TooltipContent>
				</Tooltip>
			)}
		</div>
	);
}

function RowMenu({
	token,
	canDuplicate,
	canRevoke,
	onDuplicate,
	onRevoke,
}: {
	token: Token;
	canDuplicate: boolean;
	canRevoke: boolean;
	onDuplicate: () => void;
	onRevoke: () => void;
}) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={<Button variant="ghost" size="icon-sm" aria-label={`Actions for ${token.name}`} className="text-fg2" />}
			>
				<EllipsisIcon className="size-4" />
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-auto min-w-[200px]">
				<DropdownMenuGroup>
					<DropdownMenuLabel className="font-mono">{token.name}</DropdownMenuLabel>
					<DropdownMenuItem disabled={!canDuplicate} onClick={onDuplicate}>
						<CopyIcon />
						Duplicate
					</DropdownMenuItem>
				</DropdownMenuGroup>
				<DropdownMenuSeparator />
				<DropdownMenuItem variant="destructive" disabled={!canRevoke} onClick={onRevoke}>
					<Trash2Icon />
					Revoke
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

export function TokenTable({
	tokens,
	tree,
	now,
	isLoading,
	empty,
	canDuplicate,
	canRevoke,
	onDuplicate,
	onRevoke,
}: {
	tokens: Token[];
	tree: EntityTree;
	now: number;
	isLoading: boolean;
	empty: ReactNode;
	canDuplicate: boolean;
	canRevoke: boolean;
	onDuplicate: (token: Token) => void;
	onRevoke: (token: Token) => void;
}) {
	return (
		<Table className="min-w-[900px] table-fixed">
			<TableHeader>
				<TableRow>
					<TableHead className="w-[24%]">Token</TableHead>
					<TableHead>Access</TableHead>
					<TableHead className="w-[150px]">Created</TableHead>
					<TableHead className="w-[110px]">Last used</TableHead>
					<TableHead className="w-[130px]">Expires</TableHead>
					<TableHead className="w-10" />
				</TableRow>
			</TableHeader>
			<TableBody>
				{isLoading &&
					SKELETON_ROWS.map((i) => (
						<TableRow key={i}>
							<TableCell>
								<Skeleton className="h-3.5 w-36" />
							</TableCell>
							<TableCell>
								<Skeleton className="h-5 w-48" />
							</TableCell>
							<TableCell>
								<Skeleton className="h-3.5 w-20" />
							</TableCell>
							<TableCell>
								<Skeleton className="h-3.5 w-16" />
							</TableCell>
							<TableCell>
								<Skeleton className="h-3.5 w-16" />
							</TableCell>
							<TableCell />
						</TableRow>
					))}
				{!isLoading && tokens.length === 0 && (
					<TableRow>
						<TableCell colSpan={COLUMNS} className="h-auto p-0 whitespace-normal">
							{empty}
						</TableCell>
					</TableRow>
				)}
				{!isLoading &&
					tokens.map((t) => {
						const status = tokenStatus(t, now);
						const created = maybeTsMs(t.createdAt);
						return (
							<TableRow key={t.name} className={cn(status === "expired" && "opacity-65")}>
								<TableCell className="truncate font-mono text-[13px]" title={t.name}>
									{t.name}
								</TableCell>
								<TableCell className="whitespace-normal">
									<AccessCell chips={accessChips(tree, t)} />
								</TableCell>
								<TableCell className="font-mono text-[11.5px] text-fg3">
									{created === undefined ? null : relAgo(created, now)}
								</TableCell>
								<TableCell
									className={cn("font-mono text-[12px]", t.lastUsedAt === undefined ? "text-fg3" : "text-fg2")}
								>
									{lastUsedLabel(t, now)}
								</TableCell>
								<TableCell>
									<span
										className={cn(
											"flex items-center gap-2",
											status === "expired" && "text-bad-fg",
											status === "expiring" && "text-warn-fg",
											status === "active" && "text-fg2",
										)}
									>
										{status === "expired" && <StatusGlyph glyph="err" />}
										{status === "expiring" && <StatusGlyph glyph="warn" />}
										{expiresLabel(t, now)}
									</span>
								</TableCell>
								<TableCell className="text-right">
									<RowMenu
										token={t}
										canDuplicate={canDuplicate}
										canRevoke={canRevoke}
										onDuplicate={() => {
											onDuplicate(t);
										}}
										onRevoke={() => {
											onRevoke(t);
										}}
									/>
								</TableCell>
							</TableRow>
						);
					})}
			</TableBody>
		</Table>
	);
}
