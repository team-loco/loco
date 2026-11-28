import { useAuth } from "@/auth/auth-context";
import type { NewResourceForm } from "@/lib/new-resource";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import {
	addDraft,
	draftsKey,
	editDraft,
	parseDrafts,
	removeDraft,
	setDraftImage,
	setDraftName,
	type Draft,
	type DraftEdits,
	type DraftDeployment,
} from "@/lib/drafts";
import { useStoredJSON } from "@/lib/storage";

export function useDrafts(orgId: string, workspaceId: string, environmentId: string | undefined) {
	const scopes = useMyScopes();
	const { user } = useAuth();
	const canWrite = !scopes.isLoading && scopes.workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.WRITE);
	const scopeReady = environmentId !== undefined && user !== null;
	const key = canWrite && scopeReady ? draftsKey(user.id, orgId, workspaceId, environmentId) : null;
	const [drafts, update] = useStoredJSON(key, parseDrafts);
	const change = (fn: (drafts: Draft[]) => Draft[]) =>
		update((all) => {
			const next = fn(all);
			return next.length === 0 ? null : next;
		});
	return {
		drafts,
		setDeployment: (id: string, deployment: DraftDeployment | null, form?: NewResourceForm) => {
			let found = false;
			const saved = change((all) =>
				all.map((d) => {
					if (d.id !== id) return d;
					found = true;
					const { deployment: _prior, ...draft } = d;
					if (deployment === null) return draft;
					if (form === undefined) return { ...draft, deployment };
					const { image: _image, name: _name, slug: _slug, ...edits } = form;
					return { ...draft, edits, deployment };
				}),
			);
			return saved && found;
		},
		create: (draft: Draft) => change((all) => addDraft(all, draft)),
		edit: (id: string, edits: DraftEdits) => change((all) => editDraft(all, id, edits)),
		setImage: (id: string, image: string) => change((all) => setDraftImage(all, id, image)),
		setName: (id: string, name: string) => change((all) => setDraftName(all, id, name)),
		discard: (id: string) => change((all) => removeDraft(all, id)),
	};
}
