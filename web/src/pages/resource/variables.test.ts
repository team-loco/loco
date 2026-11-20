import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentPlacementSchema, DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { referenceKey, regionsWithDifferentVariables, resolveReferences } from "./variables";

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

describe("resolveReferences", () => {
	const ctx = {
		environment: "production",
		secrets: new Set(["DATABASE_URL"]),
		outputs: new Map([
			[
				"api",
				new Map([
					["PRIVATE_URL", "http://api:8080"],
					["DSN", "postgres://app:${{ secrets.DATABASE_URL }}@db"],
				]),
			],
		]),
	};
	test("shows what a reference resolves to and hides secrets", () => {
		expect(resolveReferences("info", ctx)).toBeNull();
		expect(resolveReferences("${{ api.PRIVATE_URL }}", ctx)).toEqual({ tone: "ok", text: "→ http://api:8080" });
		expect(resolveReferences("${{ secrets.DATABASE_URL }}", ctx)).toEqual({
			tone: "secret",
			text: "secret in production, value hidden",
		});
		expect(resolveReferences("${{ api.PRIVATE_URL }}/v1?k=${{secrets.DATABASE_URL}}", ctx)).toEqual({
			tone: "ok",
			text: "→ http://api:8080/v1?k=••••",
		});
		expect(resolveReferences("${{ api.DSN }}", ctx)).toEqual({ tone: "ok", text: "→ postgres://app:••••@db" });
	});
	test("names the reference that does not resolve", () => {
		expect(resolveReferences("${{ secrets.NOPE }}", ctx)).toEqual({
			tone: "bad",
			text: "No secret named NOPE in production",
		});
		expect(resolveReferences("${{ cart.PORT }}", ctx)).toEqual({ tone: "bad", text: "No resource named cart" });
		expect(resolveReferences("${{ api.PORT }}", ctx)).toEqual({ tone: "bad", text: "api doesn’t provide PORT" });
	});
});

describe("referenceKey", () => {
	test("keeps a secret's name and prefixes an output with its resource", () => {
		expect(referenceKey("secrets", "STRIPE_KEY")).toBe("STRIPE_KEY");
		expect(referenceKey("auth-api", "PRIVATE_URL")).toBe("AUTH_API_URL");
		expect(referenceKey("api", "PORT")).toBe("API_PORT");
	});
});
