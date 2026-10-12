const outdir = "dist";
const port = 4173;

const server = Bun.serve({
	hostname: "localhost",
	port,
	async fetch(request) {
		const path = new URL(request.url).pathname;
		const file = Bun.file(`${outdir}${path}`);
		const found = path !== "/" && (await file.exists());
		return new Response(found ? file : Bun.file(`${outdir}/index.html`));
	},
});
console.log(`Loco UI build on ${server.url.href}`);
