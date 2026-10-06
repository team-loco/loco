import { BoxIcon, Building2Icon, LayersIcon, ShieldIcon } from "lucide-react";

import { Badge } from "@/components/design/Badge";
import { Skeleton } from "@/components/design/Skeleton";
import { cn } from "@/lib/utils";

import { MemberAvatar } from "./MemberAvatar";
import { ScopeBadge } from "./scopes";
import type { EntityKind, Member, ScopeEntry } from "./useTeamData";

export function EntityIcon({ kind, className }: { kind: EntityKind; className?: string | undefined }) {
	const cls = cn("size-3.5 shrink-0 text-fg3", className);
	switch (kind) {
		case "system":
			return <ShieldIcon className={cls} />;
		case "org":
			return <Building2Icon className={cls} />;
		case "workspace":
			return <LayersIcon className={cls} />;
		case "resource":
			return <BoxIcon className={cls} />;
	}
}

function EntryChip({ entry }: { entry: ScopeEntry }) {
	const label = entry.parent !== "" ? `${entry.parent}/${entry.label}` : entry.label;
	return (
		<span className="inline-flex max-w-full min-w-0 items-center gap-1 rounded-sm border border-line bg-background py-0.5 pr-0.5 pl-1.5">
			<EntityIcon kind={entry.kind} className="size-3" />
			<span className="truncate text-sm">{label}</span>
			{entry.scopes.map((s) => (
				<ScopeBadge key={s} scope={s} />
			))}
		</span>
	);
}

export function MembersTable({
	members,
	meId,
	selectedId,
	onSelect,
	isLoading,
	scopesLoading,
	compact,
}: {
	members: Member[];
	meId: string;
	selectedId: string | null;
	onSelect: (id: string | null) => void;
	isLoading: boolean;
	scopesLoading: boolean;
	compact: boolean;
}) {
	const cols = compact
		? "grid-cols-1 md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]"
		: "grid-cols-1 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]";

	return (
		<section className="min-w-0 overflow-hidden rounded-lg border border-line bg-background">
			<div
				className={cn(
					"grid h-9 items-center gap-4 border-b border-line bg-bg2 px-4 text-sm font-semibold text-fg2",
					cols,
				)}
			>
				<span>Member</span>
				<span className="hidden md:inline">Access</span>
			</div>
			{isLoading &&
				[0, 1, 2, 3].map((i) => (
					<div key={i} className={cn("grid items-center gap-4 border-b border-line px-4 py-2.5", cols)}>
						<div className="flex items-center gap-2.5">
							<Skeleton className="size-7 rounded-full" />
							<div className="flex flex-col gap-1">
								<Skeleton className="h-3.5 w-28" />
								<Skeleton className="h-3 w-40" />
							</div>
						</div>
						<Skeleton className="hidden h-5 w-48 md:block" />
					</div>
				))}
			{!isLoading &&
				members.map((m) => {
					const on = selectedId === m.user.id;
					const isMe = m.user.id === meId;
					return (
						<button
							key={m.user.id}
							type="button"
							onClick={() => {
								onSelect(on ? null : m.user.id);
							}}
							className={cn(
								"grid w-full cursor-pointer items-center gap-x-4 gap-y-2 border-b border-l-[3px] border-b-line py-2.5 pr-4 pl-[13px] text-left last:border-b-0 hover:bg-bg2",
								on ? "border-l-primary bg-info-bg hover:bg-info-bg" : "border-l-transparent",
								cols,
							)}
						>
							<div className="flex min-w-0 items-center gap-2.5">
								<MemberAvatar name={m.user.name} email={m.user.email} avatarUrl={m.user.avatarUrl} />
								<div className="flex min-w-0 flex-col">
									<span className="flex items-center gap-1.5 font-semibold whitespace-nowrap">
										<span className="truncate">{m.user.name || m.user.email}</span>
										{isMe && (
											<Badge tone="muted" size="sm" className="text-fg2">
												You
											</Badge>
										)}
									</span>
									<span className="truncate text-sm text-fg3">{m.user.email}</span>
								</div>
							</div>
							<div className="flex min-w-0 flex-wrap items-center gap-1.5">
								{scopesLoading && m.entries.length === 0 && <Skeleton className="h-5 w-32" />}
								{m.entries.map((e) => (
									<EntryChip key={`${e.kind}:${e.id}`} entry={e} />
								))}
								{!scopesLoading && m.entries.length === 0 && <span className="text-fg4">—</span>}
							</div>
						</button>
					);
				})}
			{!isLoading && members.length === 0 && <div className="px-4 py-7 text-fg3">No members match.</div>}
		</section>
	);
}
