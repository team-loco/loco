import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { SizeLimitsSchema } from "@gen/loco/config/v1/config_pb";

import { sizeOptions } from "./sizing";

const MI = 1_048_576n;

test("sizeOptions offers only the presets inside the API's limits", () => {
	const options = sizeOptions(
		create(SizeLimitsSchema, {
			minCpuMillis: 250,
			maxCpuMillis: 1000,
			minMemoryBytes: 256n * MI,
			maxMemoryBytes: 1024n * MI,
			minReplicas: 1,
			maxReplicas: 10,
		}),
	);
	expect(options.cpu.map((o) => o.value)).toEqual(["250m", "500m", "1"]);
	expect(options.memory.map((o) => o.value)).toEqual(["256Mi", "512Mi", "1Gi"]);
	expect(options.memory.map((o) => o.label)).toEqual(["256 MiB", "512 MiB", "1 GiB"]);
	expect(options.replicas).toEqual({ min: 1, max: 10 });
});

test("sizeOptions offers nothing before the limits load", () => {
	const options = sizeOptions(undefined);
	expect(options.cpu).toEqual([]);
	expect(options.memory).toEqual([]);
	expect(options.replicas).toBeNull();
});
