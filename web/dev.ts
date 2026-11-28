import index from "./index.html";
import { stagingProxy } from "./dev-proxy";

const publicDir = "public";
const proxyURL = Bun.env["LOCO_UI_PROXY_URL"];
const proxyKey = Bun.env["LOCO_UI_PROXY_KEY"];

const port = Number(Bun.env["WEB_PORT"]);
if (!Number.isInteger(port)) {
	throw new Error("WEB_PORT is not set; start the UI with mise run ui");
}

const publicFiles = await Array.fromAsync(new Bun.Glob("**/*").scan(publicDir));
const publicRoutes = Object.fromEntries(
	publicFiles.map((file) => [`/${file}`, () => new Response(Bun.file(`${publicDir}/${file}`))]),
);

const routes: Bun.Serve.Routes<undefined, string> = {
	...publicRoutes,
	"/*": index,
};
if (proxyURL) {
	if (!proxyKey) throw new Error("Missing staging proxy key");
	routes["/api/*"] = stagingProxy({ upstream: proxyURL, key: proxyKey });
}

const server = Bun.serve({
	hostname: "localhost",
	port,
	development: { hmr: true, console: true },
	...(proxyURL ? { idleTimeout: 0 } : {}),
	routes,
});
console.log(`Loco UI on ${server.url.href}`);
