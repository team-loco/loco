import { renderToStaticMarkup } from "react-dom/server";

import { NotFound } from "@/pages/NotFound";

export function renderNotFoundPage(css: string, logoUrl: string): string {
	return `<!doctype html>${renderToStaticMarkup(
		<html lang="en">
			<head>
				<meta charSet="UTF-8" />
				<meta name="viewport" content="width=device-width, initial-scale=1.0" />
				<meta name="robots" content="noindex" />
				<link rel="icon" href="data:," />
				{/* Escaping would corrupt the CSS; it is compiled from this repo by build.ts. */}
				{/* eslint-disable-next-line @eslint-react/dom-no-dangerously-set-innerhtml */}
				<style dangerouslySetInnerHTML={{ __html: css }} />
			</head>
			<body>
				<NotFound staticPage={{ logoUrl }} />
			</body>
		</html>,
	)}`;
}
