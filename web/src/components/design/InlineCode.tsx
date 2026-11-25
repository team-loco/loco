import { Fragment, type ReactElement, type ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Code, commands and names (a workspace, an environment) inside running text. */
export function InlineCode({ children, className }: { children: ReactNode; className?: string }) {
	return (
		<code
			className={cn(
				"inline rounded-[4px] bg-panel2 px-1 font-mono text-[0.86em] leading-[1.4] text-foreground [overflow-wrap:anywhere] box-decoration-clone",
				className,
			)}
		>
			{children}
		</code>
	);
}

/** Names joined as "a, b and c", each one as inline code; a string child ends the list as plain text, like "2 more". */
export function InlineCodeList({ names, children }: { names: readonly string[]; children?: string | undefined }) {
	let element = 0;
	return new Intl.ListFormat("en", { style: "long", type: "conjunction" })
		.formatToParts(children === undefined ? names : [...names, children])
		.map((part, i) => (
			<Fragment key={i}>
				{part.type === "element" && element++ < names.length ? <InlineCode>{part.value}</InlineCode> : part.value}
			</Fragment>
		));
}

/** Tagged template for running text: every interpolated value is shown as inline code. */
export function withCode(strings: TemplateStringsArray, ...values: ReactNode[]): ReactElement[] {
	return strings.map((text, i) => (
		<Fragment key={i}>
			{text}
			{i < values.length && <InlineCode>{values[i]}</InlineCode>}
		</Fragment>
	));
}
