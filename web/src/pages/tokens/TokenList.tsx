import type { Token } from "@gen/loco/token/v1/token_pb";

import { Skeleton } from "@/components/design/Skeleton";
import { cn } from "@/lib/utils";

import { accessLines, expiresLabel, fmtDay, LEVEL_BADGE, LEVEL_LABEL, lastUsedLabel, tokenGrants, tokenStatus, tsMillis } from "./model";
import type { EntityTree } from "./model";

const WIDE_COLS = "grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)_120px_120px]";
const NARROW_COLS = "grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_96px]";

export function TokenList({
	tokens,
	tree,
	now,
	selected,
	onSelect,
	isLoading,
	emptyLabel,
}: {
	tokens: Token[];
	tree: EntityTree;
	now: number;
	selected: string | null;
	onSelect: (name: string | null) => void;
	isLoading: boolean;
	emptyLabel: string;
}) {
	const wide = selected === null;
	const cols = wide ? WIDE_COLS : NARROW_COLS;

	return (
		<section className="min-w-0 overflow-hidden rounded-lg border border-line bg-background">
			<div className="overflow-x-auto">
				<div className={cn(wide ? "min-w-[680px]" : "min-w-[480px]")}>
					<div className={cn("grid h-9 items-center gap-4 border-b border-line bg-bg2 px-4 text-sm font-semibold text-fg2", cols)}>
						<span>Token</span>
						<span>Access</span>
						{wide && <span>Last used</span>}
						<span>Expires</span>
					</div>
					{isLoading &&
						[0, 1, 2, 3].map((i) => (
							<div key={i} className={cn("grid items-center gap-4 border-b border-line py-3 pr-4 pl-[13px] last:border-b-0", cols)}>
								<div className="flex flex-col gap-1.5 pl-[3px]">
									<Skeleton className="h-3.5 w-36" />
									<Skeleton className="h-3 w-20" />
								</div>
								<Skeleton className="h-5 w-56" />
								{wide && <Skeleton className="h-3.5 w-16" />}
								<Skeleton className="h-3.5 w-16" />
							</div>
						))}
					{!isLoading &&
						tokens.map((t) => (
							<TokenRow
								key={t.name}
								token={t}
								tree={tree}
								now={now}
								wide={wide}
								cols={cols}
								active={selected === t.name}
								onSelect={onSelect}
							/>
						))}
					{!isLoading && tokens.length === 0 && <div className="px-4 py-7 text-fg3">{emptyLabel}</div>}
				</div>
			</div>
		</section>
	);
}

function TokenRow({
	token,
	tree,
	now,
	wide,
	cols,
	active,
	onSelect,
}: {
	token: Token;
	tree: EntityTree;
	now: number;
	wide: boolean;
	cols: string;
	active: boolean;
	onSelect: (name: string | null) => void;
}) {
	const status = tokenStatus(token, now);
	const grants = tokenGrants(token);
	const lines = accessLines(tree, grants);
	const created = tsMillis(token.createdAt);
	const used = token.lastUsedAt !== undefined;

	return (
		<button
			type="button"
			onClick={() => {
				onSelect(active ? null : token.name);
			}}
			className={cn(
				"grid w-full cursor-pointer items-center gap-4 border-b border-l-[3px] border-line py-3 pr-4 pl-[13px] text-left hover:bg-bg2",
				cols,
				active ? "border-l-primary bg-info-bg hover:bg-info-bg" : "border-l-transparent",
				status === "expired" && "opacity-65",
			)}
		>
			<div className="flex min-w-0 flex-col gap-[3px]">
				<span className="truncate font-semibold">{token.name}</span>
				<span className="text-[11.5px] text-fg3">{created === null ? "—" : `Created ${fmtDay(created)}`}</span>
			</div>
			<div className="flex min-w-0 flex-col gap-1">
				{lines.map((l) => (
					<div key={l.level} className="flex min-w-0 items-center gap-2">
						<span
							className={cn(
								"inline-flex h-5 w-[50px] shrink-0 items-center justify-center rounded-sm text-xs font-semibold",
								LEVEL_BADGE[l.level],
							)}
						>
							{LEVEL_LABEL[l.level]}
						</span>
						<span className="truncate">{l.names}</span>
					</div>
				))}
			</div>
			{wide && <span className={used ? "text-foreground" : "text-fg3"}>{lastUsedLabel(token, now)}</span>}
			<span
				className={cn(
					"flex items-center gap-1.5 whitespace-nowrap",
					status === "expired" && "font-medium text-bad-fg",
					status === "expiring" && "font-medium text-warn-fg",
					status === "active" && "text-fg2",
				)}
			>
				{status === "expired" && <span className="size-[7px] rounded-full bg-err" />}
				{status === "expiring" && <span className="size-[7px] rounded-full bg-warn" />}
				{expiresLabel(token, now)}
			</span>
		</button>
	);
}
