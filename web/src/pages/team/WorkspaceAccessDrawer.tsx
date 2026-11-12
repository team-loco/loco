import { useMutation } from "@connectrpc/connect-query";
import { createMember, deleteMember } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useState } from "react";

import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { LEVEL_LABEL } from "@/hooks/useMyScopes";
import { toastConnectError } from "@/lib/error-handler";
import { usePagedRows } from "@/lib/paging";
import { userLabel } from "@/lib/user";
import { cn } from "@/lib/utils";

import { MemberAvatar } from "./MemberAvatar";
import { LEVELS, levelBlocked, levelOf, levelPlan, type Level, type Member, type ScopeName } from "./model";

const PAGE_SIZES = [25] as const;
const PRESSED: Record<Level, string> = {
	0: "data-pressed:text-fg3 aria-pressed:text-fg3",
	1: "",
	2: "data-pressed:text-info-fg aria-pressed:text-info-fg",
	3: "data-pressed:text-warn-fg aria-pressed:text-warn-fg",
};
type Show = "has" | "all";

export function WorkspaceAccessDrawer({
	member,
	workspaces,
	holds,
	onClose,
	onChanged,
}: {
	member: Member | null;
	workspaces: Workspace[];
	holds: (workspaceId: string, scope: ScopeName) => boolean;
	onClose: () => void;
	onChanged: () => Promise<void>;
}) {
	const [shown, setShown] = useState<Member | null>(member);
	if (member !== null && member !== shown) setShown(member);
	const user = shown?.member.user;
	return (
		<Sheet
			open={member !== null}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<SheetContent size="wide">
				<SheetHeader>
					<MemberAvatar
						name={user?.name ?? ""}
						email={user?.email ?? ""}
						avatarUrl={user?.avatarUrl ?? ""}
						className="size-[34px] shrink-0"
					/>
					<span className="flex min-w-0 flex-col gap-0.5">
						<SheetTitle className="truncate">{user === undefined ? "" : userLabel(user)}</SheetTitle>
						<SheetDescription className="truncate font-mono text-[12px]">{user?.email}</SheetDescription>
					</span>
				</SheetHeader>
				{shown !== null && (
					<AccessRows
						key={shown.member.user?.id}
						member={shown}
						workspaces={workspaces}
						holds={holds}
						onChanged={onChanged}
					/>
				)}
			</SheetContent>
		</Sheet>
	);
}

function AccessRows({
	member,
	workspaces,
	holds,
	onChanged,
}: {
	member: Member;
	workspaces: Workspace[];
	holds: (workspaceId: string, scope: ScopeName) => boolean;
	onChanged: () => Promise<void>;
}) {
	const create = useMutation(createMember);
	const remove = useMutation(deleteMember);
	const [busy, setBusy] = useState<string | null>(null);
	const [show, setShow] = useState<Show>(member.workspaces.length > 0 ? "has" : "all");
	const [q, setQ] = useState("");
	const userId = member.member.user?.id ?? "";
	const granted = new Set(member.workspaces.map((a) => a.id));
	const query = q.trim().toLowerCase();
	const rows = workspaces.filter(
		(w) => (show === "all" || granted.has(w.id)) && (query === "" || w.name.toLowerCase().includes(query)),
	);
	const paged = usePagedRows(rows, PAGE_SIZES);

	const apply = async (workspaceId: string, current: ScopeName[], level: Level) => {
		const plan = levelPlan(current, level);
		if (plan === null) return;
		setBusy(workspaceId);
		try {
			if (plan.revokeAll) await remove.mutateAsync({ workspaceId, userId });
			if (plan.grant.length > 0) await create.mutateAsync({ workspaceId, userId, scopes: plan.grant });
		} catch (err) {
			toastConnectError(err, "Failed to change workspace access");
		} finally {
			await onChanged();
			setBusy(null);
		}
	};

	return (
		<>
			<div className="flex flex-wrap items-center gap-2.5 border-b border-line px-6 py-3">
				<ToggleGroup
					variant="segmented"
					aria-label="Show workspaces"
					value={[show]}
					onValueChange={(v: string[]) => {
						if (v[0] === "has" || v[0] === "all") {
							setShow(v[0]);
							paged.reset();
						}
					}}
				>
					<ToggleGroupItem value="has" count={granted.size}>
						Has access
					</ToggleGroupItem>
					<ToggleGroupItem value="all" count={workspaces.length}>
						All workspaces
					</ToggleGroupItem>
				</ToggleGroup>
				<SearchInput
					value={q}
					onChange={(e) => {
						setQ(e.target.value);
						paged.reset();
					}}
					onClear={() => {
						setQ("");
						paged.reset();
					}}
					placeholder="Find a workspace"
					aria-label="Find a workspace"
					className="ml-auto min-w-0 flex-[1_1_120px]"
				/>
			</div>
			<SheetBody className="gap-0 pt-1">
				{rows.length === 0 && (
					<span className="py-6 text-center text-fg3">
						{query !== "" ? "No workspace matches." : "No access to any workspace yet."}
					</span>
				)}
				<ul className="m-0 flex list-none flex-col p-0">
					{paged.rows.map((w) => {
						const current = member.workspaces.find((a) => a.id === w.id)?.scopes ?? [];
						const level = levelOf(current);
						const held = (s: ScopeName) => holds(w.id, s);
						return (
							<li
								key={w.id}
								className="flex min-h-11 flex-wrap items-center gap-3 border-b border-line py-1.5 last:border-b-0"
							>
								<span className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{w.name}</span>
								<ToggleGroup
									variant="segmented"
									aria-label={`Access to ${w.name}`}
									value={[level.toString()]}
									onValueChange={(v: string[]) => {
										const next = LEVELS.find((l) => l.toString() === v[0]);
										if (next !== undefined) void apply(w.id, current, next);
									}}
								>
									{LEVELS.map((l) => {
										const reason = levelBlocked(current, l, held);
										return (
											<ToggleGroupItem
												key={l}
												value={l.toString()}
												disabled={busy !== null || reason !== null}
												title={reason ?? undefined}
												className={cn("disabled:opacity-45", PRESSED[l])}
											>
												{LEVEL_LABEL[l]}
											</ToggleGroupItem>
										);
									})}
								</ToggleGroup>
							</li>
						);
					})}
				</ul>
			</SheetBody>
			<Pager {...paged.pager} className="border-t border-line px-6" />
		</>
	);
}
