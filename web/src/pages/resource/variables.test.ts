import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { createRouterTransport } from "@connectrpc/connect";
import { createQueryOptions } from "@connectrpc/connect-query";
import { QueryClient } from "@tanstack/react-query";
import { listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";
import { SecretSchema } from "@gen/loco/secret/v1/secret_pb";

import { refreshSecrets, secretRows, secretUsageMessage, unusedNames, yamlBindingHint } from "./variables";

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
