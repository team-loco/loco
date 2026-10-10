import type { CSSProperties } from "react";
import { CheckIcon, CopyIcon } from "lucide-react";
import { highlight, type LanguageName } from "sugar-high";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";
import { cn } from "@/lib/utils";

const COPIED_RESET_MS = 2000;

interface CodeProps {
	code: string;
	language?: LanguageName | undefined;
	className?: string | undefined;
}

function Code({ code, language = "plaintext", className }: CodeProps) {
	const html = highlight(code, { lang: language });
	return <code className={cn("font-mono", className)} dangerouslySetInnerHTML={{ __html: html }} />;
}

interface CodeBlockProps {
	children: string;
	language?: LanguageName | undefined;
	className?: string | undefined;
}

function CodeBlock({ children, language = "plaintext", className }: CodeBlockProps) {
	const [copiedKey, copy] = useCopy(COPIED_RESET_MS);
	const copied = copiedKey !== null;
	const lineCount = children.split("\n").length;
	const digits = lineCount.toString().length;
	const gutter = { "--code-digits": digits } as CSSProperties;

	return (
		<div
			className={cn(
				"group/code relative flex min-h-0 flex-col overflow-hidden rounded-lg border border-line bg-code-bg",
				className
			)}
		>
			<pre
				tabIndex={0}
				style={gutter}
				className={
					"m-0 min-h-0 overflow-auto py-3 font-mono text-sm leading-[1.6] text-(--sh-identifier) outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
				}
			>
				<Code code={children} language={language} className="code-lines" />
			</pre>
			<Button
				variant="ghost"
				size="icon-xs"
				aria-label={copied ? "Copied" : "Copy code"}
				className={cn(
					"absolute top-1.5 right-1.5 border-line bg-code-bg/85 backdrop-blur-xs hover:bg-bg3 dark:hover:bg-bg3",
					"opacity-0 group-focus-within/code:opacity-100 group-hover/code:opacity-100 pointer-coarse:opacity-100",
					copied ? "text-ok-fg opacity-100" : "text-fg3 hover:text-foreground"
				)}
				onClick={() => {
					copy("code", children);
				}}
			>
				{copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
			</Button>
		</div>
	);
}

export { Code, CodeBlock, type CodeBlockProps, type CodeProps };
