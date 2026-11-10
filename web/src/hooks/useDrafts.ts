import { Scope } from "@gen/loco/token/v1/token_pb";

import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { addDraft, draftsKey, editDraft, parseDrafts, removeDraft, type Draft, type DraftEdits } from "@/lib/drafts";
import { useStoredJSON } from "@/lib/storage";

export function useDrafts(orgId: string, workspaceId: string, environmentId: string | undefined) {
	const scopes = useMyScopes();
	const canWrite = !scopes.isLoading && scopes.workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.WRITE);
	const key = canWrite && environmentId !== undefined ? draftsKey(orgId, workspaceId, environmentId) : null;
	const [drafts, update] = useStoredJSON(key, parseDrafts);
	const change = (fn: (drafts: Draft[]) => Draft[]) =>
		update((all) => {
			const next = fn(all);
			return next.length === 0 ? null : next;
		});
	return {
		drafts,
		create: (draft: Draft) => change((all) => addDraft(all, draft)),
		edit: (id: string, edits: DraftEdits) => change((all) => editDraft(all, id, edits)),
		discard: (id: string) => change((all) => removeDraft(all, id)),
	};
}
