import { expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { DeploymentService, type ResolveImageRequest } from "@gen/loco/deployment/v1/deployment_pb";
import { imageResolutionOptions } from "./useImageCheck";

const digest = "registry.example.test/app@sha256:" + "a".repeat(64);

test("shares an in-flight image resolution and its digest within the same workspace and image", async () => {
	const requests: ResolveImageRequest[] = [];
	const transport = createRouterTransport((router) => {
		router.service(DeploymentService, {
			resolveImage(request) {
				requests.push(request);
				return { image: digest };
			},
		});
	});
	const client = new QueryClient();
	try {
		const checked = client.query(imageResolutionOptions(transport, "workspace", " app:latest "));
		const deployed = client.query(imageResolutionOptions(transport, "workspace", "app:latest"));
		expect((await checked).image).toBe(digest);
		expect((await deployed).image).toBe(digest);
		expect((await client.query(imageResolutionOptions(transport, "workspace", "app:latest"))).image).toBe(digest);
		expect(requests.map((request) => [request.workspaceId, request.image])).toEqual([["workspace", "app:latest"]]);
		await client.query(imageResolutionOptions(transport, "workspace", "app:other"));
		await client.query(imageResolutionOptions(transport, "other", "app:latest"));
		expect(requests.map((request) => [request.workspaceId, request.image])).toEqual([
			["workspace", "app:latest"],
			["workspace", "app:other"],
			["other", "app:latest"],
		]);
	} finally {
		client.clear();
	}
});

test("a failed image check can resolve successfully on a later deployment attempt", async () => {
	let calls = 0;
	const transport = createRouterTransport((router) => {
		router.service(DeploymentService, {
			resolveImage() {
				calls++;
				if (calls === 1) throw new ConnectError("Registry is unavailable", Code.Unavailable);
				return { image: digest };
			},
		});
	});
	const client = new QueryClient();
	try {
		const options = imageResolutionOptions(transport, "workspace", "app:latest");
		expect(await client.query(options).catch(String)).toContain("Registry is unavailable");
		expect((await client.query(options)).image).toBe(digest);
		expect(calls).toBe(2);
	} finally {
		client.clear();
	}
});
