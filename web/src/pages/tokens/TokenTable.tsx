import { CopyIcon, Trash2Icon } from "lucide-react";
import type { ReactNode } from "react";
import type { Token } from "@gen/loco/token/v1/token_pb";

import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { LevelChip } from "@/components/design/LevelChip";
import { RowMenu } from "@/components/design/RowMenu";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { LEVEL_LABEL } from "@/hooks/useMyScopes";
import { agoLabel, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { accessByLevel, accessChips, expiresLabel, lastUsedLabel, tokenStatus } from "./model";
import type { AccessChip, Creator, EntityTree } from "./model";

function AccessCell({ chips }: { chips: AccessChip[] }) {
	return (
		<div className="flex min-w-0 flex-wrap gap-1 *:shrink-0">
			{accessByLevel(chips).map((g) => {
				const names = g.nodes.map((n) => (n.kind === "ws" ? `${n.label} workspace` : n.label)).join(", ");
				return <LevelChip key={g.level} level={g.level} name={names} title={`${LEVEL_LABEL[g.level]} ${names}`} />;
			})}
		</div>
	);
}

export function CreatorCell({ token, creator, now }: { token: Token; creator: Creator | undefined; now: number }) {
	const created = maybeTsMs(token.createdAt);
	const name = token.createdBy === "" ? "Unknown" : (creator?.name ?? "") || token.createdBy;
	return (
		<span className="flex min-w-0 items-center gap-2.5" title={token.createdBy}>
			<MemberAvatar name={name} email="" avatarUrl={creator?.avatarUrl ?? ""} className="size-[22px] shrink-0" />
			<span className="flex min-w-0 flex-col gap-px">
				<span className="truncate text-[13px] text-foreground">{name}</span>
				{created !== undefined && <span className="font-mono text-[11px] text-fg3">{agoLabel(created, now)}</span>}
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

function tokenColumns({
	creators,
	tree,
	now,
	canDuplicate,
	canRevoke,
	onDuplicate,
	onRevoke,
}: {
	creators: ReadonlyMap<string, Creator>;
	tree: EntityTree;
	now: number;
	canDuplicate: boolean;
	canRevoke: boolean;
	onDuplicate: (token: Token) => void;
	onRevoke: (token: Token) => void;
}): DataColumn<Token>[] {
	return [
		{
			id: "token",
			header: "Token",
			size: 220,
			minSize: 120,
			meta: { fit: true, fitMax: 220 },
			cell: ({ row: { original: t } }) => (
				<span className="flex min-w-0 flex-col gap-px font-mono text-[13px]">
					<CellText text={t.name} />
					{t.displaySuffix !== "" && <span className="text-[11px] text-fg3">loco_k_…{t.displaySuffix}</span>}
				</span>
			),
		},
		{
			id: "access",
			header: "Access",
			size: 200,
			minSize: 100,
			meta: { fit: true, fitMax: 320 },
			cell: ({ row: { original: t } }) => <AccessCell chips={accessChips(tree, t)} />,
		},
		{
			id: "creator",
			header: "Created by",
			size: 190,
			minSize: 100,
			meta: { grow: true },
			cell: ({ row: { original: t } }) => <CreatorCell token={t} creator={creators.get(t.createdBy)} now={now} />,
		},
		{
			id: "used",
			header: "Last used",
			size: 110,
			minSize: 80,
			cell: ({ row: { original: t } }) => (
				<span className={cn("font-mono text-[12px]", t.lastUsedAt === undefined ? "text-fg3" : "text-fg2")}>
					{lastUsedLabel(t, now)}
				</span>
			),
		},
		{
			id: "expires",
			header: "Expires",
			size: 130,
			minSize: 90,
			cell: ({ row: { original: t } }) => {
				const status = tokenStatus(t, now);
				return (
					<span
						className={cn(
							"flex min-w-0 items-center gap-2",
							status === "expired" && "text-bad-fg",
							status === "expiring" && "text-warn-fg",
							status === "active" && "text-fg2",
						)}
					>
						{status === "expired" && <StatusGlyph glyph="err" />}
						{status === "expiring" && <StatusGlyph glyph="warn" />}
						<CellText text={expiresLabel(t, now)} />
					</span>
				);
			},
		},
		{
			id: "menu",
			enableResizing: false,
			meta: { align: "end", pinEnd: true, label: "Actions" },
			cell: ({ row: { original: t } }) => (
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
			),
		},
	];
}

export function TokenTable({
	tokens,
	isLoading,
	empty,
	now,
	...columns
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
	const table = useDataTable("tokens", {
		columns: tokenColumns({ ...columns, now }),
		data: tokens,
		getRowId: (t) => t.name,
		enableSorting: false,
		manualPagination: true,
	});
	return (
		<DataTable
			table={table}
			label="Tokens"
			loading={isLoading}
			empty={empty}
			rowClassName={(row) => tokenStatus(row.original, now) === "expired" && "opacity-65"}
		/>
	);
}
