import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { DeploymentActorType, DeploymentMethod, DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentActor, deploymentMethod } from "./deployment-utils";

test("deployment attribution names the caller and how they deployed", () => {
	const deployment = create(DeploymentSchema, {
		actorType: DeploymentActorType.USER,
		actorName: "nikumar1206",
		method: DeploymentMethod.CLI,
	});
	expect([deploymentActor(deployment), deploymentMethod(deployment)]).toEqual(["@nikumar1206", "loco deploy · CLI"]);
	deployment.actorType = DeploymentActorType.API_TOKEN;
	deployment.actorName = "release-token";
	deployment.method = DeploymentMethod.API_TOKEN;
	expect([deploymentActor(deployment), deploymentMethod(deployment)]).toEqual(["release-token", "API token"]);
	deployment.actorName = "";
	expect(deploymentActor(deployment)).toBe("Deleted token");
});
