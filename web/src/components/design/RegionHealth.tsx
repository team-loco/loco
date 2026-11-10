import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "./Badge";

export function RegionHealth({ region }: { region: RegionInfo | undefined }) {
	const status = region?.healthStatus ?? "";
	if (status === "" || status === "healthy") return null;
	return (
		<Badge size="sm" tone={status === "degraded" ? "warn" : "bad"}>
			{status}
		</Badge>
	);
}
