import { expect, test } from "bun:test";

// This fixture is synthetic. Live staging verification is separate.
test("parallel staging dev servers preserve compressed responses and quiet streams", async () => {
	const key = "synthetic-bridge-key";
	const upstream = Bun.serve({
		hostname: "localhost",
		port: 0,
		idleTimeout: 0,
		fetch(request) {
			if (request.headers.get("authorization") !== `Bearer ${key}`)
				return new Response("wrong credential", { status: 401 });
			if (request.headers.has("cookie")) return new Response("cookie leaked", { status: 400 });
			if (new URL(request.url).pathname === "/compressed") {
				return new Response(Bun.gzipSync('{"ok":true}'), {
					headers: { "content-type": "application/json", "content-encoding": "gzip", "set-cookie": "synthetic=value" },
				});
			}
			let timer: ReturnType<typeof setTimeout>;
			return new Response(
				new ReadableStream({
					start(controller) {
						controller.enqueue(new TextEncoder().encode("first\n"));
						timer = setTimeout(() => {
							controller.enqueue(new TextEncoder().encode("second\n"));
							controller.close();
						}, 12_000);
					},
					cancel() {
						clearTimeout(timer);
					},
				}),
			);
		},
	});
	const children = Array.from({ length: 2 }, () =>
		Bun.spawn([process.execPath, "dev.ts"], {
			cwd: import.meta.dir,
			env: { ...process.env, WEB_PORT: "0", LOCO_UI_PROXY_URL: upstream.url.origin, LOCO_UI_PROXY_KEY: key },
			stdout: "pipe",
			stderr: "inherit",
		}),
	);
	try {
		const origins: string[] = [];
		for (const child of children) {
			const reader = child.stdout.getReader();
			let startup = "";
			while (!startup.includes("Loco UI on")) {
				const chunk = await reader.read();
				if (chunk.done) throw new Error(`Dev server exited: ${startup}`);
				startup += new TextDecoder().decode(chunk.value);
			}
			reader.releaseLock();
			const match = /Loco UI on (http:\/\/localhost:\d+)\//.exec(startup);
			if (!match?.[1]) throw new Error(`Missing dev server URL: ${startup}`);
			origins.push(match[1]);
		}
		expect(new Set(origins).size).toBe(2);
		for (const current of origins) {
			const response = await fetch(`${current}/api/compressed`, { headers: { origin: current } });
			expect(response.status).toBe(200);
			expect(await response.json()).toEqual({ ok: true });
		}
		const origin = origins[0];
		if (!origin) throw new Error("Missing first server URL");
		const response = await fetch(`${origin}/api/compressed`, { headers: { origin, cookie: "synthetic=browser" } });
		expect(response.status).toBe(200);
		expect(response.headers.has("set-cookie")).toBe(false);
		expect(await response.json()).toEqual({ ok: true });
		const foreign = await fetch(`${origin}/api/compressed`, { headers: { origin: "https://example.com" } });
		expect(foreign.status).toBe(403);
		const crossInstance = await fetch(`${origin}/api/compressed`, { headers: { origin: origins[1] ?? "" } });
		expect(crossInstance.status).toBe(403);
		const sameOrigin = await fetch(`${origin}/api/compressed`, { headers: { "sec-fetch-site": "same-origin" } });
		expect(await sameOrigin.json()).toEqual({ ok: true });
		const started = performance.now();
		const stream = await fetch(`${origin}/api/stream`, { headers: { origin } });
		if (!stream.body) throw new Error("Missing stream body");
		const chunks = (stream.body as ReadableStream<Uint8Array>).getReader();
		const first = await chunks.read();
		let text = new TextDecoder().decode(first.value);
		expect(text).toBe("first\n");
		expect(performance.now() - started).toBeLessThan(5_000);
		for (;;) {
			const chunk = await chunks.read();
			if (chunk.done) break;
			text += new TextDecoder().decode(chunk.value);
		}
		expect(text).toBe("first\nsecond\n");
	} finally {
		for (const child of children) child.kill();
		await Promise.all(children.map(async (child) => await child.exited));
		await upstream.stop(true);
	}
}, 20_000);
