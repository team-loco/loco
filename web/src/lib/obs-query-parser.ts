import type { MessageInitShape } from "@bufbuild/protobuf";
import type { QueryLogsRequestSchema } from "@gen/loco/observability/v1/observability_pb";

/** The log filters QueryLogsRequest and TailLogsRequest share besides workspace, environment and resource scope. */
export type ParsedQuery = Pick<
	MessageInitShape<typeof QueryLogsRequestSchema>,
	| "search"
	| "levels"
	| "excludedLevels"
	| "labels"
	| "excludedLabels"
	| "logAttributes"
	| "excludedLogAttributes"
	| "jsonFields"
	| "excludedJsonFields"
	| "traceIds"
	| "excludedTraceIds"
	| "regions"
	| "excludedRegions"
	| "excludedResourceIds"
>;
