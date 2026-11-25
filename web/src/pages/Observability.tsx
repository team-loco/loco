import { useQueryState } from "nuqs";

import { Page } from "@/components/design/Page";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { observabilityViewParser, type ObservabilityView } from "@/lib/routes";

import { EmptyWorkspace } from "./dashboard/EmptyWorkspace";
import { ObsProvider, useObs } from "./observability/context";
import { EventsView } from "./observability/EventsView";
import { LogsView } from "./observability/LogsView";
import { MetricsView } from "./observability/MetricsView";
import { SystemMetricsView } from "./observability/SystemMetricsView";
import { TracesView } from "./observability/TracesView";
import { ViewHeader } from "./observability/ViewHeader";

export function ViewBody({ view }: { view: ObservabilityView }) {
	const { noResources, installation } = useObs();
	// Events also record what happens before the first deploy; every other view reads from a deployed resource.
	if (view !== "events" && noResources) {
		return (
			<>
				<ViewHeader />
				<EmptyWorkspace wholeWorkspace />
			</>
		);
	}
	switch (view) {
		case "logs":
			return <LogsView />;
		case "metrics":
			return installation ? <SystemMetricsView /> : <MetricsView />;
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
