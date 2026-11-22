import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
	createMember,
	deleteMember,
	listOrgWorkspaces,
} from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { keepPreviousData } from "@tanstack/react-query";
import { useState } from "react";

import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { Tip } from "@/components/design/Tooltip";
import { LEVEL_LABEL } from "@/hooks/useMyScopes";
import { toastConnectError } from "@/lib/error-handler";
import { useTokenPage } from "@/hooks/useTokenPage";
import { formatTotal, pagedRows, tokenPageLabel, tokenPager, type Paging } from "@/lib/paging";
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
	orgId,
	member,
	holds,
	onClose,
	onChanged,
}: {
	orgId: string;
	member: Member | null;
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
						className="size-[26px] shrink-0"
					/>
					<span className="flex min-w-0 items-baseline gap-2">
						<SheetTitle className="shrink-0">{user === undefined ? "" : userLabel(user)}</SheetTitle>
						<SheetDescription className="truncate font-mono text-[12px]">{user?.email}</SheetDescription>
					</span>
				</SheetHeader>
				{shown !== null && (
					<AccessRows key={shown.member.user?.id} orgId={orgId} member={shown} holds={holds} onChanged={onChanged} />
				)}
			</SheetContent>
		</Sheet>
	);
}

function AccessRows({
	orgId,
	member,
	holds,
	onChanged,
}: {
	orgId: string;
	member: Member;
	holds: (workspaceId: string, scope: ScopeName) => boolean;
	onChanged: () => Promise<void>;
}) {
	const create = useMutation(createMember);
	const remove = useMutation(deleteMember);
	const [busy, setBusy] = useState<string | null>(null);
	const [show, setShow] = useState<Show>(member.workspaces.length > 0 ? "has" : "all");
	const [q, setQ] = useState("");
	const userId = member.member.user?.id ?? "";
	const query = q.trim();
	const granted = member.workspaces.filter((w) => w.name.toLowerCase().includes(query.toLowerCase()));
	const [paging, setPaging] = useState<Paging>({ page: 0, size: PAGE_SIZES[0] });
	const grantedPage = pagedRows(granted, paging, setPaging, PAGE_SIZES);
	const [tokens, setTokens] = useState<string[]>([]);
	const all = useTokenPage(listOrgWorkspaces, { orgId, pageSize: PAGE_SIZES[0], query }, tokens, {
		enabled: show === "all",
		placeholderData: keepPreviousData,
	});
	const allCount = useQuery(listOrgWorkspaces, {
		orgId,
		pageSize: 1,
		pageToken: "",
		includeTotalCount: true,
		query: "",
	});
	const allRows = all.query.data?.workspaces ?? [];
	const allPager = tokenPager(tokens, all.nextPageToken, (next) => {
		setTokens(next ?? []);
	});
	const rows = show === "has" ? grantedPage.rows : allRows;
	const pager =
		show === "has"
			? grantedPage.pager
			: {
					...allPager,
					label: tokenPageLabel(allPager.page, PAGE_SIZES[0], allRows.length, all.total),
					total: allRows.length,
					pageSizes: PAGE_SIZES,
				};
	const loading = show === "all" && all.query.isPending;
	const resetPage = () => {
		setPaging((current) => ({ ...current, page: 0 }));
		setTokens([]);
	};

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
							resetPage();
						}
					}}
				>
					<ToggleGroupItem value="has" count={member.workspaces.length}>
						Has access
					</ToggleGroupItem>
					<ToggleGroupItem
						value="all"
						count={allCount.data === undefined ? undefined : formatTotal(allCount.data.totalCount)}
					>
						All workspaces
					</ToggleGroupItem>
				</ToggleGroup>
				<SearchInput
					value={q}
					onChange={(e) => {
						setQ(e.target.value);
						resetPage();
					}}
					onClear={() => {
						setQ("");
						resetPage();
					}}
					placeholder="Search workspaces"
					aria-label="Search workspaces"
					className="ml-auto min-w-0 flex-[1_1_120px]"
				/>
			</div>
			<SheetBody className="gap-0 pt-1">
				{rows.length === 0 && !loading && (
					<span className="py-6 text-center text-fg3">
						{query !== "" ? "No workspace matches." : "No access to any workspace yet."}
					</span>
				)}
				<ul className="m-0 flex list-none flex-col p-0">
					{rows.map((w) => {
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
											<Tip key={l} tip={reason}>
												<ToggleGroupItem
													value={l.toString()}
													disabled={busy !== null || reason !== null}
													className={cn("disabled:opacity-45", PRESSED[l])}
												>
													{LEVEL_LABEL[l]}
												</ToggleGroupItem>
											</Tip>
										);
									})}
								</ToggleGroup>
							</li>
						);
					})}
				</ul>
			</SheetBody>
			<Pager {...pager} className="border-t border-line px-6" />
		</>
	);
}
