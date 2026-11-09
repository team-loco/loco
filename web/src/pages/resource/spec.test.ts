import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import {
	DeploymentSchema,
	DeploymentSpecSchema,
	ServiceDeploymentSpecSchema,
	type Deployment,
} from "@gen/loco/deployment/v1/deployment_pb";

import { diffLines, sideBySide, specChanges } from "./spec";

function deployment(image: string, replicas: number, env: Record<string, string>): Deployment {
	return create(DeploymentSchema, {
		replicas,
		spec: create(DeploymentSpecSchema, {
			spec: {
				case: "service",
				value: create(ServiceDeploymentSpecSchema, { build: { type: "image", image }, cpu: "250m", port: 8080, env }),
			},
		}),
	});
}

describe("sideBySide", () => {
	test("pairs removed and added lines and numbers each side", () => {
		const rows = sideBySide(diffLines("a\nb\nc", "a\nB\nc\nd").lines);
		expect(rows).toEqual([
			{ left: { n: 1, text: "a", changed: false }, right: { n: 1, text: "a", changed: false } },
			{ left: { n: 2, text: "b", changed: true }, right: { n: 2, text: "B", changed: true } },
			{ left: { n: 3, text: "c", changed: false }, right: { n: 3, text: "c", changed: false } },
			{ left: null, right: { n: 4, text: "d", changed: true } },
		]);
	});
});

describe("specChanges", () => {
	test("lists what differs between two deployments", () => {
		const from = deployment("ghcr.io/acme/shop:1.4.0", 2, { LOG_LEVEL: "info", OLD: "1" });
		const to = deployment("ghcr.io/acme/shop:1.5.0", 3, { LOG_LEVEL: "debug" });
		expect(specChanges(from, to)).toEqual([
			{ label: "image", from: "shop:1.4.0", to: "shop:1.5.0" },
			{ label: "replicas", from: "2", to: "3" },
			{ label: "env.LOG_LEVEL", from: "info", to: "debug" },
			{ label: "env.OLD", from: "1", to: "—" },
		]);
	});

	test("is empty for the same spec", () => {
		const dep = deployment("ghcr.io/acme/shop:1.4.0", 2, {});
		expect(specChanges(dep, dep)).toEqual([]);
	});
});
