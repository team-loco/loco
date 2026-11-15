import { Links, Meta, Outlet, Scripts, ScrollRestoration } from "react-router";
import { RootProvider } from "fumadocs-ui/provider/react-router";
import SearchDialog from "./components/search";
import "./app.css";
import favicon from "../../web/public/favicon.svg?url";

export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <link rel="icon" href={favicon} />
        {import.meta.env.VITE_DOCS_ENVIRONMENT === "staging" && (
          <meta name="robots" content="noindex, nofollow" />
        )}
        <Meta />
        <Links />
      </head>
      <body className="flex flex-col min-h-screen">
        <RootProvider search={{ SearchDialog }}>{children}</RootProvider>
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  return <Outlet />;
}
