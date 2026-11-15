export function loader() {
  return new Response(
    `User-agent: *\n${import.meta.env.VITE_DOCS_ENVIRONMENT === "staging" ? "Disallow" : "Allow"}: /\nSitemap: ${import.meta.env.VITE_DOCS_URL}sitemap.xml\n`,
    { headers: { "Content-Type": "text/plain" } },
  );
}
