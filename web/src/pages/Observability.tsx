import { useQueryState } from "nuqs";

import { Page } from "@/components/design/Page";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { observabilityViewParser, type ObservabilityView } from "@/lib/routes";

import { EmptyWorkspace } from "./dashboard/EmptyWorkspace";
import { ObsProvider, useObs } from "./observability/context";
import { EventsView } from "./observability/EventsView";
import { LogsView } from "./observability/LogsView";
import { MetricsView } from "./observability/MetricsView";
import { TracesView } from "./observability/TracesView";

function ViewBody({ view }: { view: ObservabilityView }) {
	const { resources, resourcesLoading, scopedResourceIds } = useObs();
	// Events also record what happens before the first deploy; every other view reads from a deployed resource.
	if (view !== "events" && !resourcesLoading && scopedResourceIds === null && resources.length === 0) {
		return <EmptyWorkspace />;
	}
	switch (view) {
		case "logs":
			return <LogsView />;
		case "metrics":
			return <MetricsView />;
		case "traces":
			return <TracesView />;
		case "events":
			return <EventsView />;
	}
}

export function Observability() {
	const [view] = useQueryState("view", observabilityViewParser);
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();

	if (activeOrgId === null || activeWorkspaceId === null) return null;

	return (
		<Page className="max-w-[1440px] gap-4">
			<ObsProvider key={view} orgId={activeOrgId} workspaceId={activeWorkspaceId} view={view}>
				<ViewBody view={view} />
			</ObsProvider>
		</Page>
	);
}
