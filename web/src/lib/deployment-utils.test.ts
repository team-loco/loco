import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { DeploymentActorType, DeploymentMethod, DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentOrigin } from "./deployment-utils";

test("deployment attribution shows the caller, credential and optional source commit", () => {
	const deployment = create(DeploymentSchema, {
		actorType: DeploymentActorType.USER,
		actorName: "nikumar1206",
		method: DeploymentMethod.CLI,
		commit: { sha: "7c41e9a".padEnd(40, "0"), dirty: true },
	});
	expect(deploymentOrigin(deployment)).toBe("@nikumar1206 · loco deploy · 7c41e9a (uncommitted changes)");
	deployment.actorType = DeploymentActorType.API_TOKEN;
	deployment.actorName = "release-token";
	deployment.method = DeploymentMethod.API_TOKEN;
	deployment.commit = { ...deployment.commit!, dirty: false };
	expect(deploymentOrigin(deployment)).toBe("release-token · API token · 7c41e9a");
	deployment.actorType = DeploymentActorType.USER;
	deployment.actorName = "Olivia Owner";
	deployment.method = DeploymentMethod.WEB;
	deployment.commit = undefined;
	expect(deploymentOrigin(deployment)).toBe("@Olivia Owner · Dashboard");
});
