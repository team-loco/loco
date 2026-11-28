import type { MessageInitShape } from "@bufbuild/protobuf";
import type { CreateDeploymentRequestSchema } from "@gen/loco/deployment/v1/deployment_pb";
import type { Deployment, DeploymentTrigger } from "@gen/loco/deployment/v1/deployment_pb";

import { getErrorMessage } from "@/lib/error-handler";
import { toast } from "@/lib/toast";

import { depTag, isDeployed, type RegionView } from "./model";
import { InlineCodeList, withCode } from "@/components/design/InlineCode";

/** A deployment to run again and the regions it runs in. */
export interface DeploymentSource {
	deployment: Deployment;
	regions: string[];
}

/** The running deployments of the regions that serve one, with the regions each one serves. */
export function currentSources(regions: readonly RegionView[]): DeploymentSource[] {
	const sources = new Map<string, DeploymentSource>();
	for (const region of regions.filter(isDeployed)) {
		const deployment = region.current;
		if (deployment === undefined) continue;
		const source = sources.get(deployment.id) ?? { deployment, regions: [] };
		source.regions.push(region.name);
		sources.set(deployment.id, source);
	}
	return [...sources.values()];
}

/** Creates a deployment from each source and returns one message per source that failed. */
export async function createDeployments(
	create: (request: MessageInitShape<typeof CreateDeploymentRequestSchema>) => Promise<unknown>,
	resourceId: string,
	sources: readonly DeploymentSource[],
	trigger: DeploymentTrigger,
): Promise<string[]> {
	const failures: string[] = [];
	for (const { deployment: d, regions } of sources) {
		try {
			await create({
				resourceId,
				regions,
				environmentId: d.environmentId,
				trigger,
				sourceDeploymentId: d.id,
			});
		} catch (err) {
			failures.push(`${regions.join(", ")}: ${getErrorMessage(err, "Failed to create deployment")}`);
		}
	}
	return failures;
}

export function toastRedeploying(resourceName: string, sources: readonly DeploymentSource[]) {
	const tags = [...new Set(sources.map((s) => depTag(s.deployment)))];
	toast.info(withCode`Redeploying ${resourceName}`, {
		description: (
			<>
				New deployment from <InlineCodeList names={tags} /> in{" "}
				<InlineCodeList names={sources.flatMap((s) => s.regions)} />.
			</>
		),
	});
}
