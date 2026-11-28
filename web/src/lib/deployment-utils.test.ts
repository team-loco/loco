import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { DeploymentActorType, DeploymentMethod, DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentActor, deploymentMethod, deploymentBuild, deploymentImage } from "./deployment-utils";

test("deployment attribution names the caller and how they deployed", () => {
	const deployment = create(DeploymentSchema, {
		actorType: DeploymentActorType.USER,
		actorName: "Nikhil Kumar",
		method: DeploymentMethod.CLI,
	});
	expect([deploymentActor(deployment), deploymentMethod(deployment)]).toEqual(["Nikhil Kumar", "CLI"]);
	deployment.actorType = DeploymentActorType.API_TOKEN;
	deployment.actorName = "release-token";
	deployment.method = DeploymentMethod.API_TOKEN;
	expect([deploymentActor(deployment), deploymentMethod(deployment)]).toEqual(["release-token", "API token"]);
	deployment.actorName = "";
	expect(deploymentActor(deployment)).toBe("Deleted token");
});

test("deployment labels read the same build from summary and full views", () => {
	const build = {
		type: "dockerfile",
		image: "example.test/app:release",
		buildId: "source-build",
	};
	const summary = create(DeploymentSchema, { build });
	const full = create(DeploymentSchema, {
		spec: { spec: { case: "service", value: { build } } },
	});
	for (const deployment of [summary, full]) {
		expect(deploymentImage(deployment)).toBe(build.image);
		expect(deploymentBuild(deployment)?.buildId).toBe(build.buildId);
	}
});
