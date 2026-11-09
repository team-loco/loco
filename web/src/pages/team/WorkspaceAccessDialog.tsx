import { useMutation } from "@connectrpc/connect-query";
import { createMember, deleteMember } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { useState } from "react";

import { Dialog, DialogBody, DialogContent, DialogDescription } from "@/components/design/Dialog";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { toastConnectError } from "@/lib/error-handler";

import {
	LEVEL_LABEL,
	LEVELS,
	levelBlocked,
	levelOf,
	levelPlan,
	type Level,
	type Member,
	type ScopeName,
} from "./model";

export function WorkspaceAccessDialog({
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
	const name = user === undefined ? "" : user.name || user.email;
	return (
		<Dialog
			open={member !== null}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<DialogContent title="Workspace access">
				<DialogBody>
					<DialogDescription>What {name} can do in each workspace. Changes apply right away.</DialogDescription>
					{shown !== null && <AccessRows member={shown} workspaces={workspaces} holds={holds} onChanged={onChanged} />}
				</DialogBody>
			</DialogContent>
		</Dialog>
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
	const userId = member.member.user?.id ?? "";

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

	if (workspaces.length === 0) return <span className="text-fg3">This organization has no workspaces yet.</span>;

	return (
		<ul className="m-0 flex max-h-[min(420px,60dvh)] list-none flex-col overflow-y-auto p-0">
			{workspaces.map((w) => {
				const current = member.workspaces.find((a) => a.id === w.id)?.scopes ?? [];
				const level = levelOf(current);
				const held = (s: ScopeName) => holds(w.id, s);
				return (
					<li key={w.id} className="flex flex-wrap items-center gap-3 border-b border-line py-2.5 last:border-b-0">
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
										className="disabled:opacity-45"
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
	);
}
