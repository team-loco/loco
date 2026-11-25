import { Fragment, type ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Code, commands and names (a workspace, an environment) inside running text. */
export function InlineCode({ children, className }: { children: ReactNode; className?: string }) {
	return (
		<code
			className={cn(
				"inline rounded-[4px] bg-panel2 px-1.5 font-mono text-[0.86em] leading-[1.4] whitespace-nowrap text-foreground",
				className,
			)}
		>
			{children}
		</code>
	);
}

/** Names joined as "a, b and c", each one as inline code; `extra` closes the list as plain text, like "2 more". */
export function InlineCodeList({ names, extra }: { names: readonly string[]; extra?: string }) {
	const items = extra === undefined ? names : [...names, extra];
	return new Intl.ListFormat("en", { style: "long", type: "conjunction" })
		.formatToParts(items)
		.map((part, i) => (
			<Fragment key={i}>
				{part.type === "element" && part.value !== extra ? <InlineCode>{part.value}</InlineCode> : part.value}
			</Fragment>
		));
}

/** Tagged template for running text: every interpolated value is shown as inline code. */
export function withCode(strings: TemplateStringsArray, ...values: ReactNode[]) {
	return strings.map((text, i) => (
		<Fragment key={i}>
			{text}
			{i < values.length && <InlineCode>{values[i]}</InlineCode>}
		</Fragment>
	));
}
