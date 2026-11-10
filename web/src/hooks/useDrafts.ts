import { useState } from "react";
import type { EnvRow } from "@/lib/new-resource";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { addDraft, draftsKey, editDraft, parseDrafts, removeDraft, type Draft, type DraftEdits } from "@/lib/drafts";
import { useStoredJSON } from "@/lib/storage";

export function useDrafts(orgId: string, workspaceId: string, environmentId: string | undefined) {
	const scopes = useMyScopes();
	const canWrite = !scopes.isLoading && scopes.workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.WRITE);
	const key = canWrite && environmentId !== undefined ? draftsKey(orgId, workspaceId, environmentId) : null;
	const [stored, update] = useStoredJSON(key, parseDrafts);
	const [values, setValues] = useState<ReadonlyMap<string, EnvRow[]>>(() => new Map());
	const drafts = stored.map((draft) => {
		const vars = values.get(`${key}:${draft.id}`);
		return vars === undefined ? draft : { ...draft, edits: { ...draft.edits, vars } };
	});
	const change = (fn: (drafts: Draft[]) => Draft[]) =>
		update((all) => {
			const next = fn(all);
			return next.length === 0 ? null : next;
		});
	return {
		drafts,
		create: (draft: Draft) => change((all) => addDraft(all, draft)),
		edit: (id: string, edits: DraftEdits) => {
			if (edits.vars !== undefined) {
				const vars = edits.vars;
				setValues((current) => new Map(current).set(`${key}:${id}`, vars));
			}
			return change((all) => editDraft(all, id, edits));
		},
		discard: (id: string) => change((all) => removeDraft(all, id)),
	};
}
