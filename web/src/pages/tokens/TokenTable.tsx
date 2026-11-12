import { CopyIcon, Trash2Icon } from "lucide-react";
import type { ReactNode } from "react";
import type { Token } from "@gen/loco/token/v1/token_pb";

import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { LevelChip } from "@/components/design/LevelChip";
import { RowMenu } from "@/components/design/RowMenu";
import { Skeleton } from "@/components/design/Skeleton";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { LEVEL_LABEL } from "@/hooks/useMyScopes";
import { maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { accessByLevel, accessChips, expiresLabel, lastUsedLabel, relAgo, tokenStatus } from "./model";
import type { AccessChip, Creator, EntityTree } from "./model";

const SKELETON_ROWS = [0, 1, 2, 3];
const COLUMNS = 6;

function AccessCell({ chips }: { chips: AccessChip[] }) {
	return (
		<div className="flex min-w-0 flex-wrap gap-1">
			{accessByLevel(chips).map((g) => {
				const names = g.nodes.map((n) => n.label).join(", ");
				return <LevelChip key={g.level} level={g.level} name={names} title={`${LEVEL_LABEL[g.level]} ${names}`} />;
			})}
		</div>
	);
}

function CreatorCell({ token, creator, now }: { token: Token; creator: Creator | undefined; now: number }) {
	const created = maybeTsMs(token.createdAt);
	const name = token.createdBy === "" ? "Unknown" : (creator?.name ?? "") || token.createdBy;
	return (
		<span className="flex min-w-0 items-center gap-2.5" title={token.createdBy}>
			<MemberAvatar name={name} email="" avatarUrl={creator?.avatarUrl ?? ""} className="size-[22px] shrink-0" />
			<span className="flex min-w-0 flex-col gap-px">
				<span className="truncate text-[13px] text-foreground">{name}</span>
				{created !== undefined && <span className="font-mono text-[11px] text-fg3">{relAgo(created, now)}</span>}
			</span>
		</span>
	);
}

function TokenMenu({
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
		<RowMenu label={`Actions for ${token.name}`} title={<span className="font-mono">{token.name}</span>}>
			<DropdownMenuItem disabled={!canDuplicate} onClick={onDuplicate}>
				<CopyIcon />
				Duplicate
			</DropdownMenuItem>
			<DropdownMenuSeparator />
			<DropdownMenuItem variant="destructive" disabled={!canRevoke} onClick={onRevoke}>
				<Trash2Icon />
				Revoke
			</DropdownMenuItem>
		</RowMenu>
	);
}

export function TokenTable({
	tokens,
	creators,
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
	creators: ReadonlyMap<string, Creator>;
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
		<Table className="min-w-[1050px] table-fixed">
			<TableHeader>
				<TableRow>
					<TableHead className="w-[24%]">Token</TableHead>
					<TableHead>Access</TableHead>
					<TableHead className="w-[190px]">Created by</TableHead>
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
								<Skeleton className="h-3.5 w-24" />
							</TableCell>
							<TableCell>
								<Skeleton className="h-3.5 w-16" />
							</TableCell>
							<TableCell>
								<Skeleton className="h-3.5 w-20" />
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
						return (
							<TableRow key={t.name} className={cn(status === "expired" && "opacity-65")}>
								<TableCell className="truncate font-mono text-[13px]" title={t.name}>
									{t.name}
								</TableCell>
								<TableCell className="whitespace-normal">
									<AccessCell chips={accessChips(tree, t)} />
								</TableCell>
								<TableCell>
									<CreatorCell token={t} creator={creators.get(t.createdBy)} now={now} />
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
									<TokenMenu
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
