import { isValidElement, type ReactNode } from "react";

type Props = { children?: ReactNode };

/** The plain text of a node, as a screen reader would join it; function components are called with their props. */
export function textOf(node: ReactNode): string {
	if (typeof node === "string" || typeof node === "number") return String(node);
	if (Array.isArray(node)) return node.map(textOf).join("");
	if (!isValidElement<Props>(node)) return "";
	if (typeof node.type === "function") return textOf((node.type as (props: Props) => ReactNode)(node.props));
	return textOf(node.props.children);
}
