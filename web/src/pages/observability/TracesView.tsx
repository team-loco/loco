import { WaypointsIcon } from "lucide-react";

import { EmptyState } from "@/components/design/EmptyState";
import { SoonTag } from "@/components/design/SoonTag";

export function TracesView() {
	return (
		<section className="rounded-lg border border-dashed border-line2">
			<EmptyState
				icon={<WaypointsIcon />}
				title={
					<span className="flex items-center gap-2">
						Tracing isn't collected yet
						<SoonTag />
					</span>
				}
			>
				Distributed traces across your resources are coming. Logs already carry trace ids where your app emits them.
			</EmptyState>
		</section>
	);
}
