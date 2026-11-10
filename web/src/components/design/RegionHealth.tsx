import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "./Badge";

export function RegionHealth({ region }: { region: RegionInfo | undefined }) {
	const reported = region?.healthStatus ?? "";
	const status = reported === "" ? "unknown" : reported;
	return (
		<Badge size="sm" tone={status === "healthy" ? "ok" : status === "unknown" ? "muted" : "warn"}>
			{status}
		</Badge>
	);
}
