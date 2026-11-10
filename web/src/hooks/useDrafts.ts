import { useSyncExternalStore } from "react";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { addDraft, draftsKey, editDraft, parseDrafts, removeDraft, type Draft, type DraftEdits } from "@/lib/drafts";
import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
	listeners.add(listener);
	window.addEventListener("storage", listener);
	return () => {
		listeners.delete(listener);
		window.removeEventListener("storage", listener);
	};
}

function save(key: string, change: (drafts: Draft[]) => Draft[]): boolean {
	const next = change(parseDrafts(readStorage(key)));
	let saved = true;
	if (next.length === 0) removeStorage(key);
	else saved = writeStorage(key, JSON.stringify(next));
	for (const listener of listeners) listener();
	return saved;
}

export function useDrafts(orgId: string, workspaceId: string, environmentId: string | undefined) {
	const scopes = useMyScopes();
	const canWrite = !scopes.isLoading && scopes.workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.WRITE);
	const key = canWrite && environmentId !== undefined ? draftsKey(orgId, workspaceId, environmentId) : null;
	const raw = useSyncExternalStore(subscribe, () => (key === null ? null : readStorage(key)));
	const drafts = parseDrafts(raw);
	const change = (fn: (drafts: Draft[]) => Draft[]) => key !== null && save(key, fn);
	return {
		drafts,
		create: (draft: Draft) => change((all) => addDraft(all, draft)),
		edit: (id: string, edits: DraftEdits) => change((all) => editDraft(all, id, edits)),
		discard: (id: string) => change((all) => removeDraft(all, id)),
	};
}
