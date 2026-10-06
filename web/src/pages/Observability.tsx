import { useSearchParams } from "react-router";

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
		case "logs":
			return "logs";
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
		<div className="flex w-full max-w-[1440px] min-w-0 flex-col gap-4 px-4 pt-2 pb-16 md:px-8">
			<ObsProvider orgId={activeOrgId} workspaceId={activeWorkspaceId} view={view}>
				<ViewBody view={view} />
			</ObsProvider>
		</div>
	);
}
