import type { MessageInitShape } from "@bufbuild/protobuf";
import type { LogFieldFilterSchema } from "@gen/loco/observability/v1/observability_pb";

export interface ParsedQuery {
	regions?: string[];
	excludedRegions?: string[];
	search: string;
	levels: string[];
	labels: MessageInitShape<typeof LogFieldFilterSchema>[];
}
