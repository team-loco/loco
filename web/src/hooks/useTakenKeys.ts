import { useQuery } from "@tanstack/react-query";

import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import type { Draft } from "@/lib/drafts";

export interface TakenKeys {
	names: ReadonlySet<string>;
	slugs: ReadonlySet<string>;
	/** The name of the resource that holds a draft's name or slug, ignoring case in names. */
	holder: (draft: Draft) => string | null;
}

/** The names and slugs the workspace's resources and drafts hold, leaving out resource `except`. */
export function useTakenKeys(
	orgId: string,
	workspaceId: string,
	drafts: readonly Draft[] = [],
	except?: string,
): TakenKeys {
	const resourcesQuery = useQuery(useWorkspaceResourcesOptions(orgId, workspaceId));
	const resources = (resourcesQuery.data?.resources ?? []).filter((r) => r.id !== except);
	return {
		names: new Set([...resources.map((r) => r.name), ...drafts.map((d) => d.name)]),
		slugs: new Set([...resources.map((r) => r.slug), ...drafts.map((d) => d.slug)]),
		holder: (draft) =>
			resources.find((r) => r.slug === draft.slug || r.name.toLowerCase() === draft.name.toLowerCase())?.name ?? null,
	};
}
