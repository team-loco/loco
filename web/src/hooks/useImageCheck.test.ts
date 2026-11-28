import { expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { DeploymentService, type ResolveImageRequest } from "@gen/loco/deployment/v1/deployment_pb";
import { imageResolutionOptions, resolveDraftImage } from "./useImageCheck";

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
		const deployed = resolveDraftImage(client, transport, "workspace", "app:latest");
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

test("editing the image while deployment awaits its check does not cancel the deployment", async () => {
	const started = Promise.withResolvers<void>();
	const resolved = Promise.withResolvers<void>();
	const requests: string[] = [];
	const transport = createRouterTransport((router) => {
		router.service(DeploymentService, {
			async resolveImage(request, context) {
				requests.push(request.image);
				if (request.image === "app:latest") {
					started.resolve();
					await resolved.promise;
					context.signal.throwIfAborted();
				}
				return { image: digest };
			},
		});
	});
	const client = new QueryClient();
	const preview = new QueryObserver(client, imageResolutionOptions(transport, "workspace", "app:latest"));
	const unsubscribe = preview.subscribe(() => {});
	try {
		await started.promise;
		const deployed = resolveDraftImage(client, transport, "workspace", "app:latest");
		// React useQuery moves its observer to the newly typed image.
		preview.setOptions(imageResolutionOptions(transport, "workspace", "app:other"));
		resolved.resolve();
		expect((await deployed).image).toBe(digest);
		expect(requests.filter((image) => image === "app:latest")).toHaveLength(1);
	} finally {
		resolved.resolve();
		unsubscribe();
		client.clear();
	}
});
