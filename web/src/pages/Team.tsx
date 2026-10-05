import { Building2Icon, PlusIcon, SearchIcon } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";

import { Page, PageHeader } from "@/components/design/Page";
import { SoonTag } from "@/components/design/SoonTag";
import { Button } from "@/components/design/Button";
import { useAuth } from "@/auth/AuthProvider";
import { useBreadcrumbs } from "@/context/ShellContext";
import { useMyScopes } from "@/hooks/useMyScopes";
import { getErrorMessage } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { MemberPanel } from "./team/MemberPanel";
import { MembersTable } from "./team/MembersTable";
import { toScope } from "./team/scopes";
import { useTeamData } from "./team/useTeamData";

export function Team() {
	useBreadcrumbs("Team");
	const { orgId = "" } = useParams<{ orgId: string }>();
	const { user } = useAuth();
	const meId = user?.id ?? "";
	const scopes = useMyScopes();
	const { org, members, workspaces, isLoading, scopesLoading, error } = useTeamData(orgId, meId, scopes.scopes);
	const [q, setQ] = useState("");
	const [selectedId, setSelectedId] = useState<string | null>(null);

	const tq = q.trim().toLowerCase();
	const shown = members
		.filter((m) => tq === "" || `${m.user.name} ${m.user.email}`.toLowerCase().includes(tq))
		.sort((a, b) => {
			const aMe = a.user.id === meId ? 1 : 0;
			const bMe = b.user.id === meId ? 1 : 0;
			if (aMe !== bMe) return bMe - aMe;
			return (a.user.name || a.user.email).localeCompare(b.user.name || b.user.email);
		});
	const selected = selectedId !== null ? members.find((m) => m.user.id === selectedId) : undefined;
	const count = `${members.length.toString()} ${members.length === 1 ? "member" : "members"}`;
	const errorMessage = error ? getErrorMessage(error, "Failed to load members") : null;

	return (
		<Page className="max-w-[1360px] gap-[18px]">
			<PageHeader
				title="Team"
				actions={
					<span className="flex items-center gap-2">
						<SoonTag />
						<Button size="lg" className="px-3.5" disabled title="Invites aren't available yet">
							<PlusIcon />
							Invite
						</Button>
					</span>
				}
			/>
			<div className="flex flex-wrap items-center gap-2.5">
				<span className="flex items-center gap-2 text-fg2">
					<Building2Icon className="size-[15px] text-fg3" />
					<span className="font-semibold text-foreground">{org?.name ?? "…"}</span>
					{!isLoading && <span className="text-fg3">{count}</span>}
				</span>
				<div className="flex-1" />
				<label className="flex h-[34px] w-[260px] max-w-full items-center gap-2 rounded-lg border border-line bg-background px-2.5 focus-within:border-fg4">
					<SearchIcon className="size-3.5 text-fg3" />
					<input
						value={q}
						placeholder="Search members"
						className="min-w-0 flex-1 border-0 bg-transparent text-base text-foreground outline-none placeholder:text-fg4"
						onChange={(e) => {
							setQ(e.target.value);
						}}
					/>
				</label>
			</div>

			{errorMessage !== null ? (
				<div className="rounded-lg border border-line px-4 py-8 text-fg3">{errorMessage}</div>
			) : (
				<div
					className={cn(
						"grid items-start gap-5",
						selected ? "grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(360px,400px)]" : "grid-cols-1",
					)}
				>
					<MembersTable
						members={shown}
						meId={meId}
						selectedId={selected ? selected.user.id : null}
						onSelect={setSelectedId}
						isLoading={isLoading}
						scopesLoading={scopesLoading}
						compact={selected !== undefined}
					/>
					{selected && (
						<MemberPanel
							key={selected.user.id}
							member={selected}
							orgName={org?.name ?? ""}
							isMe={selected.user.id === meId}
							workspaces={workspaces}
							holds={(wsId, s) => scopes.holds(orgId, wsId, toScope(s))}
							onClose={() => {
								setSelectedId(null);
							}}
						/>
					)}
				</div>
			)}
		</Page>
	);
}
