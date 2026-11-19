export const DELETED_RESOURCE = "Deleted resource";

/** The name a traffic breakdown shows for a resource, and whether the resource still has a page. */
export function trafficResourceName(
	id: string,
	live: ReadonlyMap<string, string>,
	deleted: ReadonlyMap<string, string>,
): { name: string; live: boolean } {
	const name = live.get(id);
	if (name !== undefined) return { name, live: true };
	const gone = deleted.get(id);
	return { name: gone === undefined ? DELETED_RESOURCE : `${gone} (deleted)`, live: false };
}
