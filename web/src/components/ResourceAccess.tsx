import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import { Link } from "react-router";

import { EmptyState } from "@/components/design/EmptyState";
import { Section } from "@/components/design/Page";
import { pluralize } from "@/lib/format";
import { resourcePath } from "@/lib/routes";

/** What a user who reads only some resources of a workspace sees in place of workspace-wide views. */
export function ResourceAccess({
	orgId,
	workspaceId,
	resources,
}: {
	orgId: string;
	workspaceId: string;
	resources: Resource[];
}) {
	const sorted = resources.toSorted((a, b) => a.name.localeCompare(b.name));
	return (
		<Section title="Your resources" count={resources.length}>
			<EmptyState title={`You have access to ${pluralize(resources.length, "resource")} in this workspace`}>
				A workspace admin can grant you access to the rest of it.
			</EmptyState>
			{sorted.length > 0 && (
				<ul aria-label="Your resources" className="flex flex-col border-t border-line">
					{sorted.map((r) => (
						<li key={r.id} className="border-b border-line last:border-b-0">
							<Link
								to={resourcePath(orgId, workspaceId, r.id)}
								className="flex px-5 py-3 font-mono text-[13px] hover:bg-panel2"
							>
								{r.name}
							</Link>
						</li>
					))}
				</ul>
			)}
		</Section>
	);
}
