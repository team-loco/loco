import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { createRouterTransport } from "@connectrpc/connect";
import { createQueryOptions } from "@connectrpc/connect-query";
import { QueryClient } from "@tanstack/react-query";
import { listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";
import { SecretSchema } from "@gen/loco/secret/v1/secret_pb";
import { DeploymentPlacementSchema, DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import {
	refreshSecrets,
	regionsWithDifferentVariables,
	secretRows,
	secretUsageMessage,
	unusedNames,
	variableNameProblem,
	yamlBindingHint,
} from "./variables";

describe("secretRows", () => {
	test("merges stored, declared and staged names with their status", () => {
		const stored = [create(SecretSchema, { name: "API_KEY" }), create(SecretSchema, { name: "OLD_TOKEN" })];
		const rows = secretRows(stored, ["API_KEY", "DATABASE_URL"], ["NEW_ONE"]);
		expect(rows.map((r) => [r.name, r.status])).toEqual([
			["API_KEY", "set"],
			["DATABASE_URL", "missing"],
			["NEW_ONE", "new"],
			["OLD_TOKEN", "unused"],
		]);
	});

	test("leaves the status unknown until the declared names load", () => {
		const rows = secretRows([create(SecretSchema, { name: "API_KEY" })], undefined, []);
		expect(rows.map((r) => r.status)).toEqual([undefined]);
	});
});

function running(region: string, env: Record<string, string>, secretNames: string[]) {
	const current: Deployment = create(DeploymentSchema, {
		id: region,
		secretNames,
		spec: { spec: { case: "service", value: { env } } },
		placements: [create(DeploymentPlacementSchema, { region, live: true })],
	});
	return { name: region, status: undefined, history: [current], current };
}

describe("regionsWithDifferentVariables", () => {
	test("names every deployed region when one runs other plain variables", () => {
		const regions = [running("us-east-1", { MODE: "a" }, ["KEY"]), running("eu-west-1", { MODE: "b" }, ["KEY"])];
		expect(regionsWithDifferentVariables(regions)).toEqual(["eu-west-1", "us-east-1"]);
	});

	test("names every deployed region when one reads other secrets", () => {
		const regions = [running("us-east-1", { MODE: "a" }, ["KEY"]), running("eu-west-1", { MODE: "a" }, [])];
		expect(regionsWithDifferentVariables(regions)).toEqual(["eu-west-1", "us-east-1"]);
	});

	test("names none when every region runs the same variables", () => {
		const regions = [
			running("us-east-1", { A: "1", B: "2" }, ["X", "Y"]),
			running("eu-west-1", { B: "2", A: "1" }, ["Y", "X"]),
		];
		expect(regionsWithDifferentVariables(regions)).toEqual([]);
	});
});

describe("secretUsageMessage", () => {
	test("reports the saved names and regions that use them", () => {
		expect(
			secretUsageMessage("web", [
				{ region: "eu-west-1", names: ["API_KEY"] },
				{ region: "us-east-1", names: ["API_KEY", "DATABASE_URL"] },
			]),
		).toBe(
			"web uses API_KEY, DATABASE_URL in eu-west-1 and us-east-1. The new value applies on the next deploy or restart.",
		);
	});
	test("omits the message when no region uses a saved name", () => {
		expect(secretUsageMessage("web", [])).toBe("");
	});
});

describe("unusedNames", () => {
	test("keeps the saved names no region uses", () => {
		expect(unusedNames(["STRIPE_API_KEY", "API_KEY"], [{ names: ["API_KEY"] }])).toEqual(["STRIPE_API_KEY"]);
	});
});

describe("yamlBindingHint", () => {
	test("names the loco.yaml field that delivers the keys", () => {
		expect(yamlBindingHint("web", ["STRIPE_API_KEY"])).toBe(
			"Saved. To use this key, add STRIPE_API_KEY to services.web.secrets and redeploy.",
		);
		expect(yamlBindingHint("web", ["A", "B"])).toBe(
			"Saved. To use these keys, add A, B to services.web.secrets and redeploy.",
		);
	});
});

describe("variableNameProblem", () => {
	const taken = { secrets: new Set(["API_KEY"]), plain: new Set(["LOG_LEVEL"]) };
	test("lets a secret replace a stored secret but not shadow a plain variable", () => {
		expect(variableNameProblem("API_KEY", true, taken)).toBeUndefined();
		expect(variableNameProblem("LOG_LEVEL", true, taken)).toBe("LOG_LEVEL is already a plain variable.");
		expect(variableNameProblem("lower", true, taken)).toBe(
			"Use capital letters, digits and underscores, not starting with a digit.",
		);
	});
	test("accepts any environment name for a plain variable that is not taken", () => {
		expect(variableNameProblem("featureFlags", false, taken)).toBeUndefined();
		expect(variableNameProblem("API_KEY", false, taken)).toBe("API_KEY is already a variable.");
		expect(variableNameProblem("LOG_LEVEL", false, taken)).toBe("LOG_LEVEL is already a variable.");
		expect(variableNameProblem("1X", false, taken)).toBe(
			"Use letters, digits and underscores, not starting with a digit.",
		);
	});
});

describe("refreshSecrets", () => {
	const transport = createRouterTransport(() => {});
	const input = (resourceId: string) => ({ resourceId, environmentId: "env" });
	const secretsKey = (client: QueryClient, resourceId: string) => {
		const { queryKey } = createQueryOptions(listSecrets, input(resourceId), { transport });
		client.setQueryData(queryKey, {});
		return queryKey;
	};

	test("invalidates the secrets of that resource only", async () => {
		const client = new QueryClient();
		const secrets = secretsKey(client, "res");
		const other = secretsKey(client, "other");
		await refreshSecrets(client, "res", "env");
		expect(client.getQueryState(secrets)?.isInvalidated).toBe(true);
		expect(client.getQueryState(other)?.isInvalidated).toBe(false);
		client.clear();
	});
});
