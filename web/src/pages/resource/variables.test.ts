import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentPlacementSchema, DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { regionsWithDifferentVariables, variableNameProblem } from "./variables";

function running(region: string, env: Record<string, string>) {
	const current: Deployment = create(DeploymentSchema, {
		id: region,
		spec: { spec: { case: "service", value: { env } } },
		placements: [create(DeploymentPlacementSchema, { region, live: true })],
	});
	return { name: region, status: undefined, history: [current], current };
}

describe("regionsWithDifferentVariables", () => {
	test("names every deployed region when one runs other variables", () => {
		const regions = [running("us-east-1", { MODE: "a" }), running("eu-west-1", { MODE: "b" })];
		expect(regionsWithDifferentVariables(regions)).toEqual(["eu-west-1", "us-east-1"]);
	});

	test("names none when every region runs the same variables", () => {
		const regions = [running("us-east-1", { A: "1", B: "2" }), running("eu-west-1", { B: "2", A: "1" })];
		expect(regionsWithDifferentVariables(regions)).toEqual([]);
	});
});

describe("variableNameProblem", () => {
	const taken = new Set(["LOG_LEVEL"]);
	test("accepts any environment name that is not taken", () => {
		expect(variableNameProblem("featureFlags", taken)).toBeUndefined();
		expect(variableNameProblem("LOG_LEVEL", taken)).toBe("LOG_LEVEL is already a variable.");
		expect(variableNameProblem("1X", taken)).toBe("Use letters, digits and underscores, not starting with a digit.");
	});
});
