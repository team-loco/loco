import { Page } from "@/components/design/Page";
import type { SystemObservabilityView } from "@/lib/routes";

import { ViewBody } from "../Observability";
import { InstallationObsProvider } from "../observability/context";

/** Loco's own logs, metrics or traces, read through the installation scope. */
export function SystemObservability({ view }: { view: SystemObservabilityView }) {
	return (
		<Page className="max-w-[1440px] gap-4">
			<InstallationObsProvider key={view} view={view}>
				<ViewBody view={view} />
			</InstallationObsProvider>
		</Page>
	);
}
