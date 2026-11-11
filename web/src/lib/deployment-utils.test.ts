import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentOrigin } from "./deployment-utils";

test("deployment attribution shows the caller, credential and optional source commit", () => {
	const deployment = create(DeploymentSchema, {
		actorType: "user",
		actorName: "nikumar1206",
		method: "cli",
		commit: { sha: "7c41e9a".padEnd(40, "0"), dirty: true },
	});
	expect(deploymentOrigin(deployment)).toBe("@nikumar1206 · loco deploy · 7c41e9a (uncommitted changes)");
	deployment.actorType = "api_token";
	deployment.actorName = "release-token";
	deployment.method = "api_token";
	deployment.commit = { ...deployment.commit!, dirty: false };
	expect(deploymentOrigin(deployment)).toBe("release-token · API token · 7c41e9a");
	deployment.actorType = "user";
	deployment.actorName = "Olivia Owner";
	deployment.method = "web";
	deployment.commit = undefined;
	expect(deploymentOrigin(deployment)).toBe("@Olivia Owner · Dashboard");
});
