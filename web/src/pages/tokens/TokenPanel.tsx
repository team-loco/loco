import { CopyIcon, KeyRoundIcon, Trash2Icon, XIcon } from "lucide-react";
import { useState } from "react";
import type { Token } from "@gen/loco/token/v1/token_pb";

import { Button } from "@/components/design/Button";
import { cn } from "@/lib/utils";

import { ALLOWS, daysLeft, fmtDay, kindLabel, LEVEL_BAR, LEVEL_LABEL, LEVEL_TEXT, LEVELS, lastUsedLabel, resolveNode, sortKeys, tokenGrants, tokenStatus, tsMillis } from "./model";
import type { EntityNode, EntityTree, Level } from "./model";
import { kindIcon, ownerIcon } from "./icons";
import type { Owner } from "./useTokenData";

interface GrantGroup {
	id: string;
	level: Level;
	nodes: EntityNode[];
}

function groupGrants(tree: EntityTree, token: Token): GrantGroup[] {
	const grants = tokenGrants(token);
	const keys = sortKeys(tree, [...grants.keys()]);
	const groups = new Map<string, GrantGroup>();
	for (const k of keys) {
		const level = grants.get(k);
		if (level === undefined) continue;
		const node = resolveNode(tree, k);
		const scope = node.kind === "res" ? (node.parent ?? "") : k;
		const id = `${level.toString()}|${node.kind}|${scope}`;
		const g = groups.get(id);
		if (g) g.nodes.push(node);
		else groups.set(id, { id, level, nodes: [node] });
	}
	return [...groups.values()].sort((a, b) => b.level - a.level);
}

export function TokenPanel({
	token,
	owner,
	tree,
	now,
	onClose,
	onDuplicate,
	onRevoke,
}: {
	token: Token;
	owner: Owner;
	tree: EntityTree;
	now: number;
	onClose: () => void;
	onDuplicate: () => void;
	onRevoke: () => void;
}) {
	const [expanded, setExpanded] = useState<string | null>(null);
	const status = tokenStatus(token, now);
	const created = tsMillis(token.createdAt);
	const expires = tsMillis(token.expiresAt);
	const groups = groupGrants(tree, token);
	const canRevoke = owner.canCreate || owner.key === "personal";
	const ownerLabel = owner.key === "personal" ? owner.label : `${owner.label} · ${owner.noun}`;
	const expiresText =
		expires === null
			? "—"
			: status === "expired"
				? `Expired ${fmtDay(expires)}`
				: `${fmtDay(expires)} · in ${daysLeft(token, now).toString()} days`;

	return (
		<aside className="flex max-h-[calc(100vh-32px)] min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-background xl:sticky xl:top-4">
			<div className="flex items-center gap-2.5 border-b border-line py-3 pr-3 pl-4">
				<span className="flex text-fg3 [&_svg]:size-[15px]">
					<KeyRoundIcon />
				</span>
				<div className="flex min-w-0 flex-1 flex-col gap-px">
					<span className="truncate text-lg font-semibold">{token.name}</span>
					<span className="text-[11.5px] text-fg3">{owner.kind} token</span>
				</div>
				<Button variant="ghost" size="icon-sm" title="Close" className="text-fg3 hover:text-foreground" onClick={onClose}>
					<XIcon className="size-4" />
				</Button>
			</div>
			<div className="flex-1 overflow-y-auto">
				<div className="grid grid-cols-[96px_minmax(0,1fr)] gap-x-3 gap-y-[9px] border-b border-line px-4 py-3.5">
					<span className="text-fg3">Owner</span>
					<span className="flex min-w-0 items-center gap-1.5">
						<span className="flex text-fg3 [&_svg]:size-[13px]">{ownerIcon(owner.key)}</span>
						<span className="truncate">{ownerLabel}</span>
					</span>
					<span className="text-fg3">Created</span>
					<span>{created === null ? "—" : fmtDay(created)}</span>
					<span className="text-fg3">Expires</span>
					<span className={cn(status === "expired" && "text-bad-fg", status === "expiring" && "text-warn-fg")}>{expiresText}</span>
					<span className="text-fg3">Last used</span>
					<span className={token.lastUsedAt === undefined ? "text-fg3" : undefined}>{lastUsedLabel(token, now)}</span>
				</div>
				<div className="flex flex-col px-4 pt-3.5 pb-1.5">
					<span className="mb-1.5 font-semibold">Access</span>
					{groups.map((g, i) => (
						<GrantRow
							key={g.id}
							group={g}
							tree={tree}
							first={i === 0}
							expanded={expanded === g.id}
							onToggle={() => {
								setExpanded(expanded === g.id ? null : g.id);
							}}
						/>
					))}
				</div>
			</div>
			<div className="flex gap-2 border-t border-line px-4 py-3">
				<Button
					variant="outline"
					size="lg"
					className="flex-1 font-medium"
					disabled={!owner.canCreate}
					title={owner.canCreate ? undefined : `You need write access to ${owner.label} to create tokens`}
					onClick={onDuplicate}
				>
					<CopyIcon />
					Duplicate
				</Button>
				<Button
					variant="destructive-outline"
					size="lg"
					className="flex-1"
					disabled={!canRevoke}
					title={canRevoke ? undefined : `You need write access to ${owner.label} to revoke tokens`}
					onClick={onRevoke}
				>
					<Trash2Icon />
					Revoke
				</Button>
			</div>
		</aside>
	);
}

function GrantRow({
	group,
	tree,
	first,
	expanded,
	onToggle,
}: {
	group: GrantGroup;
	tree: EntityTree;
	first: boolean;
	expanded: boolean;
	onToggle: () => void;
}) {
	const n0 = group.nodes[0];
	if (n0 === undefined) return null;
	const isRes = n0.kind === "res";
	const parent = n0.parent === null ? undefined : tree.nodes.get(n0.parent);
	const total = n0.parent === null ? 0 : (tree.childCount.get(n0.parent) ?? 0);
	const count = group.nodes.length;
	const resLabel = `${count.toString()} ${count === 1 ? "resource" : "resources"}${parent === undefined ? "" : ` in ${parent.label}`}`;
	const label = isRes ? resLabel : n0.label;
	const allCurrent = count === total ? "all current" : "";
	const kind = isRes ? allCurrent : kindLabel(n0.kind);
	const chips = isRes ? (expanded ? group.nodes : group.nodes.slice(0, 10)) : [];
	const moreBtn = isRes && count > 10 ? (expanded ? "Show less" : `+${(count - 10).toString()} more`) : "";
	const allows = ALLOWS[n0.kind].slice(0, group.level).join(" · ");
	const wsChildren = tree.childCount.get(n0.key) ?? 0;
	const cascade =
		n0.kind === "org"
			? `Includes every workspace and resource in ${n0.label}`
			: n0.kind === "ws"
				? `Includes all ${wsChildren.toString()} resources in ${n0.label}, and new ones`
				: "";

	return (
		<div className={cn("flex flex-col gap-1.5 py-2.5", !first && "border-t border-line")}>
			<div className="flex items-center gap-2">
				<span className="flex text-fg3 [&_svg]:size-3.5">{kindIcon(n0.kind)}</span>
				<span className="truncate font-semibold">{label}</span>
				{kind !== "" && <span className="text-sm text-fg3">{kind}</span>}
				<div className="flex-1" />
				<span className="flex gap-0.5">
					{LEVELS.map((l) => (
						<span key={l} className={cn("h-1.5 w-3.5 rounded-[2px]", l <= group.level ? LEVEL_BAR[group.level] : "bg-bg3")} />
					))}
				</span>
				<span className={cn("w-[42px] text-right text-sm font-semibold", LEVEL_TEXT[group.level])}>{LEVEL_LABEL[group.level]}</span>
			</div>
			{isRes && (
				<div className="flex flex-wrap gap-1 pl-[22px]">
					{chips.map((c) => (
						<span key={c.key} className="inline-flex h-[22px] items-center rounded-sm bg-bg3 px-[7px] text-sm">
							{c.label}
						</span>
					))}
					{moreBtn !== "" && (
						<button
							type="button"
							onClick={onToggle}
							className="h-[22px] cursor-pointer rounded-sm px-[7px] text-sm font-medium text-primary"
						>
							{moreBtn}
						</button>
					)}
				</div>
			)}
			<span className="pl-[22px] text-[12.5px] leading-normal text-fg2">{allows}</span>
			{cascade !== "" && <span className="pl-[22px] text-sm text-fg3">{cascade}</span>}
		</div>
	);
}
