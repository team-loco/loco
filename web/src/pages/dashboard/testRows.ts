import { create } from "@bufbuild/protobuf";
import { ResourceSchema, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import type { Draft } from "@/lib/drafts";

import type { EnvResource, RegionReplicas } from "./useDashboardData";

export function draft(name: string, edits: Draft["edits"] = {}): Draft {
	return { id: `d-${name}`, name, image: `ghcr.io/acme/${name}:1`, createdMs: 1, edits };
}

export function row(
	name: string,
	patch: { description?: string; domain?: string; status?: ResourceStatus; regions?: RegionReplicas[] } = {},
): EnvResource {
	const regions = patch.regions ?? [{ region: "us-east-1", desired: 1, ready: 1 }];
	const domains = patch.domain === undefined ? [] : [{ domain: patch.domain, isPrimary: true }];
	return {
		resource: create(ResourceSchema, { id: name, name, description: patch.description ?? "", domains }),
		environment: undefined,
		status: patch.status ?? ResourceStatus.HEALTHY,
		envDeployments: [],
		last: undefined,
		regions,
		totalReplicas: regions.reduce((n, g) => n + g.desired, 0),
		neverDeployed: false,
	};
}
