import { useSearchParams } from "react-router";

import { Page } from "@/components/design/Page";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { ObservabilityView } from "@/lib/routes";

import { ObsProvider } from "./observability/context";
import { EventsView } from "./observability/EventsView";
import { LogsView } from "./observability/LogsView";
import { MetricsView } from "./observability/MetricsView";
import { TracesView } from "./observability/TracesView";

function parseView(v: string | null): ObservabilityView {
	switch (v) {
		case "metrics":
			return "metrics";
		case "traces":
			return "traces";
		case "events":
			return "events";
		case null:
			return "logs";
		default:
			return "logs";
	}
}

function ViewBody({ view }: { view: ObservabilityView }) {
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
	const [params] = useSearchParams();
	const view = parseView(params.get("view"));
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
