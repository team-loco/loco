import { useMutation } from "@connectrpc/connect-query";
import { createMember, deleteMember } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useQueryClient } from "@tanstack/react-query";
import { PlusIcon, XIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { toastConnectError } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { MemberAvatar } from "./MemberAvatar";
import { EntityIcon } from "./MembersTable";
import { parseScopeName, SCOPE_NAMES, ScopeBadge, scopeAllows, sortScopes, type ScopeName } from "./scopes";
import type { EntityKind, Member, ScopeEntry } from "./useTeamData";

const GROUPS: { kind: EntityKind; title: string }[] = [
	{ kind: "system", title: "System" },
	{ kind: "org", title: "Organization" },
	{ kind: "workspace", title: "Workspaces" },
	{ kind: "resource", title: "Resources" },
];

export function MemberPanel({
	member,
	orgName,
	isMe,
	workspaces,
	holds,
	onClose,
}: {
	member: Member;
	orgName: string;
	isMe: boolean;
	workspaces: Workspace[];
	holds: (workspaceId: string, scope: ScopeName) => boolean;
	onClose: () => void;
}) {
	const queryClient = useQueryClient();
	const create = useMutation(createMember);
	const remove = useMutation(deleteMember);
	const [busy, setBusy] = useState<string | null>(null);
	const [q, setQ] = useState("");
	const [focus, setFocus] = useState(false);

	const user = member.user;
	const displayName = user.name || user.email;

	const run = async (workspaceId: string, steps: (() => Promise<unknown>)[], fallback: string) => {
		setBusy(workspaceId);
		try {
			for (const step of steps) await step();
		} catch (err) {
			toastConnectError(err, fallback);
		} finally {
			setBusy(null);
			await queryClient.invalidateQueries();
		}
	};

	const grant = async (workspaceId: string, scopes: ScopeName[]) =>
		await create.mutateAsync({ workspaceId, userId: user.id, scopes });
	const revokeAll = async (workspaceId: string) => await remove.mutateAsync({ workspaceId, userId: user.id });

	const applyScopes = (entry: ScopeEntry, next: ScopeName[]) => {
		const added = next.filter((s) => !entry.scopes.includes(s));
		const removed = entry.scopes.filter((s) => !next.includes(s));
		if (removed.length === 0 && added.length > 0) {
			void run(entry.id, [async () => await grant(entry.id, added)], "Failed to grant scope");
			return;
		}
		if (removed.length > 0) {
			const steps: (() => Promise<unknown>)[] = [async () => await revokeAll(entry.id)];
			if (next.length > 0) steps.push(async () => await grant(entry.id, next));
			void run(entry.id, steps, "Failed to change scopes");
		}
	};

	const wsEntries = member.entries.filter((e) => e.kind === "workspace");
	const granted = new Set(wsEntries.map((e) => e.id));
	const canAddTo = (wsId: string) => !isMe && holds(wsId, "write") && holds(wsId, "read");
	const sq = q.trim().toLowerCase();
	const pool = workspaces.filter(
		(w) => !granted.has(w.id) && canAddTo(w.id) && (sq === "" || w.name.toLowerCase().includes(sq)),
	);
	const canAddAny = workspaces.some((w) => !granted.has(w.id) && canAddTo(w.id));

	const workspaceRow = (entry: ScopeEntry) => {
		const pending = busy === entry.id;
		const canWrite = holds(entry.id, "write");
		const canAdmin = holds(entry.id, "admin");
		const titleFor = (s: ScopeName, on: boolean): string => {
			if (isMe) return "You can't change your own scopes";
			if (on) {
				if (!canAdmin) return `Removing a scope requires admin on ${entry.label}`;
				const rest = entry.scopes.filter((x) => x !== s);
				const missing = rest.find((x) => !holds(entry.id, x));
				if (missing !== undefined) return `You don't hold ${missing} on ${entry.label}`;
				return `Revoke ${s}`;
			}
			if (!canWrite) return `Granting requires write on ${entry.label}`;
			if (!holds(entry.id, s)) return `You don't hold ${s} on ${entry.label}`;
			return scopeAllows(s);
		};
		const allowed = (s: ScopeName, on: boolean): boolean => {
			if (isMe || pending) return false;
			if (on) {
				const rest = entry.scopes.filter((x) => x !== s);
				return canAdmin && canWrite && rest.every((x) => holds(entry.id, x));
			}
			return canWrite && holds(entry.id, s);
		};
		const canRemove = !isMe && canAdmin && !pending;
		return (
			<div key={entry.id} className="flex min-h-9 flex-wrap items-center gap-2">
				<EntityIcon kind="workspace" />
				<span className="min-w-0 flex-1 truncate font-medium">{entry.label}</span>
				<ToggleGroup
					variant="segmented"
					multiple
					value={entry.scopes}
					onValueChange={(v: string[]) => {
						const next = sortScopes(v.map(parseScopeName).filter((s): s is ScopeName => s !== null));
						applyScopes(entry, next);
					}}
				>
					{SCOPE_NAMES.map((s) => {
						const on = entry.scopes.includes(s);
						return (
							<ToggleGroupItem
								key={s}
								value={s}
								disabled={!allowed(s, on)}
								title={titleFor(s, on)}
								className="h-[26px]! px-2.5 text-[12.5px]"
							>
								{s}
							</ToggleGroupItem>
						);
					})}
				</ToggleGroup>
				<Button
					variant="ghost"
					size="icon-xs"
					aria-label={`Remove access to ${entry.label}`}
					title={canRemove ? "Revoke all scopes" : isMe ? "You can't remove your own access" : `Requires admin on ${entry.label}`}
					className="text-fg3"
					disabled={!canRemove}
					onClick={() => {
						void run(entry.id, [async () => await revokeAll(entry.id)], "Failed to remove access");
					}}
				>
					<XIcon className="size-3.5" />
				</Button>
			</div>
		);
	};

	const readOnlyRow = (entry: ScopeEntry) => {
		const label = entry.parent !== "" ? `${entry.parent}/${entry.label}` : entry.label;
		return (
			<div key={`${entry.kind}:${entry.id}`} className="flex min-h-8 flex-wrap items-center gap-2">
				<EntityIcon kind={entry.kind} />
				<span className="min-w-0 flex-1 truncate font-medium">{label}</span>
				<span className="flex flex-wrap gap-1">
					{entry.scopes.map((s) => (
						<ScopeBadge key={s} scope={s} />
					))}
				</span>
			</div>
		);
	};

	return (
		<aside className="sticky top-4 flex max-h-[calc(100vh-32px)] flex-col overflow-hidden rounded-lg border border-line bg-background">
			<div className="flex items-center gap-3 border-b border-line py-3.5 pr-3 pl-4">
				<MemberAvatar
					name={user.name}
					email={user.email}
					avatarUrl={user.avatarUrl}
					className="size-9 [&_[data-slot=avatar-fallback]]:text-md"
				/>
				<div className="flex min-w-0 flex-1 flex-col">
					<span className="truncate text-lg font-semibold">
						{displayName}
						{isMe ? " (you)" : ""}
					</span>
					<span className="truncate text-sm text-fg3">{user.email}</span>
				</div>
				<Button variant="ghost" size="icon-sm" aria-label="Close" className="text-fg3" onClick={onClose}>
					<XIcon className="size-4" />
				</Button>
			</div>

			<div className="flex-1 overflow-y-auto">
				{GROUPS.map(({ kind, title }) => {
					const entries = member.entries.filter((e) => e.kind === kind);
					const isWs = kind === "workspace";
					if (!isWs && entries.length === 0) return null;
					return (
						<div key={kind} className="flex flex-col gap-2 border-b border-line px-4 py-3.5 last:border-b-0">
							<span className="font-semibold">
								{title} <span className="font-normal text-fg3">{entries.length}</span>
							</span>
							{isWs && entries.length === 0 && <span className="text-fg3">No workspace scopes.</span>}
							{entries.map((e) => (isWs && !isMe ? workspaceRow(e) : readOnlyRow(e)))}
							{isWs && canAddAny && (
								<div className={cn("flex flex-col overflow-hidden rounded-lg border", focus ? "border-fg4" : "border-line")}>
									<div className="flex h-[34px] items-center gap-2 px-2.5">
										<PlusIcon className="size-3.5 text-fg3" />
										<input
											value={q}
											placeholder="Add a workspace"
											autoComplete="off"
											className="min-w-0 flex-1 border-0 bg-transparent text-base text-foreground outline-none placeholder:text-fg4"
											onFocus={() => {
												setFocus(true);
											}}
											onBlur={() => {
												setFocus(false);
											}}
											onChange={(e) => {
												setQ(e.target.value);
											}}
										/>
									</div>
									{focus && (
										<div className="max-h-[200px] overflow-y-auto border-t border-line p-1">
											{pool.map((w) => (
												<button
													key={w.id}
													type="button"
													className="flex h-[30px] w-full items-center gap-2 rounded-sm px-2 text-left text-foreground hover:bg-bg3"
													onMouseDown={(e) => {
														e.preventDefault();
													}}
													onClick={() => {
														setQ("");
														void run(w.id, [async () => await grant(w.id, ["read"])], "Failed to add access");
													}}
												>
													<EntityIcon kind="workspace" className="size-[13px]" />
													<span className="min-w-0 flex-1 truncate">{w.name}</span>
													<span className="text-[11.5px] text-fg3">grants read</span>
												</button>
											))}
											{pool.length === 0 && <div className="px-2.5 py-2 text-fg3">No matches</div>}
										</div>
									)}
								</div>
							)}
						</div>
					);
				})}
			</div>

			{!isMe && (
				<div className="flex flex-col gap-1.5 border-t border-line px-4 py-3">
					<Button variant="destructive-outline" size="lg" className="w-full" disabled>
						Remove from {orgName}
					</Button>
					<span className="self-center">
						<SoonTag />
					</span>
				</div>
			)}
		</aside>
	);
}
