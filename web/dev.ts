import index from "./index.html";

const publicDir = "public";
const port = Number(Bun.env["WEB_PORT"]);
if (!Number.isInteger(port)) {
	throw new Error("WEB_PORT is not set; start the UI with mise run ui");
}

const publicFiles = await Array.fromAsync(new Bun.Glob("**/*").scan(publicDir));
const publicRoutes = Object.fromEntries(
	publicFiles.map((file) => [`/${file}`, () => new Response(Bun.file(`${publicDir}/${file}`))]),
);

const server = Bun.serve({
	hostname: "localhost",
	port,
	development: { hmr: true, console: true },
	routes: { ...publicRoutes, "/*": index },
});
console.log(`Loco UI on ${server.url.href}`);
