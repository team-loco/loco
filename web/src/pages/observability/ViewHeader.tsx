import type { ReactNode } from "react";

import { PageHeader } from "@/components/design/Page";
import { PageTitle } from "@/components/PageTitle";
import type { ObservabilityView } from "@/lib/routes";

import { useObs } from "./context";
import { TimePicker } from "./Toolbar";

export function viewTitle(view: ObservabilityView): string {
	switch (view) {
		case "logs":
			return "Logs";
		case "metrics":
			return "Metrics";
		case "traces":
			return "Traces";
		case "events":
			return "Events";
	}
}

export function ViewHeader({ actions }: { actions?: ReactNode }) {
	const { view } = useObs();
	return (
		<>
			<PageTitle title={viewTitle(view)} />
			<PageHeader
				title={viewTitle(view)}
				actions={
					<div className="flex flex-wrap items-center gap-2">
						{actions}
						<TimePicker />
					</div>
				}
			/>
		</>
	);
}
