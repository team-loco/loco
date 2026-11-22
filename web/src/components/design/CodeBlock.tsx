import { useState, type CSSProperties } from "react";
import { CheckIcon, CopyIcon, TextWrapIcon } from "lucide-react";
import { highlight, type LanguageName } from "sugar-high";

import { Button } from "@/components/design/Button";
import { ToggleChip } from "@/components/design/ToggleChip";
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
	// eslint-disable-next-line @eslint-react/dom-no-dangerously-set-innerhtml
	return <code className={cn("font-mono", className)} dangerouslySetInnerHTML={{ __html: html }} />;
}

interface CodeBlockProps {
	copyLabel?: string;
	lineNumbers?: boolean;
	wrap?: boolean;
	onWrap?: (wrap: boolean) => void;
	children: string;
	language?: LanguageName | undefined;
	className?: string | undefined;
}

function CodeBlock({
	children,
	language = "plaintext",
	className,
	copyLabel = "Copy code",
	lineNumbers = true,
	wrap = false,
	onWrap,
}: CodeBlockProps) {
	const [copiedKey, copy] = useCopy(COPIED_RESET_MS);
	const [wrapJson, setWrapJson] = useState(wrap);
	const copied = copiedKey !== null;
	const isJson = language === "json";
	const shouldWrap = isJson ? wrapJson : wrap;
	const lineCount = children.split("\n").length;
	const digits = lineCount.toString().length;
	const gutter = { "--code-digits": digits } as CSSProperties;
	const copyButton = (
		<Button
			variant="ghost"
			size="icon-xs"
			aria-label={copied ? "Copied" : copyLabel}
			className={cn(
				!isJson &&
					"absolute top-1.5 right-1.5 border-line bg-code-bg/85 opacity-0 backdrop-blur-xs group-focus-within/code:opacity-100 group-hover/code:opacity-100 pointer-coarse:opacity-100",
				copied ? "text-ok-fg opacity-100" : "text-fg3 hover:text-foreground",
			)}
			onClick={() => {
				copy("code", children);
			}}
		>
			{copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
		</Button>
	);

	return (
		<div
			data-slot="code-block"
			className={cn(
				"group/code relative flex min-h-0 flex-col overflow-hidden rounded-lg border border-line bg-code-bg",
				className,
			)}
		>
			{isJson && (
				<div className="flex shrink-0 items-center justify-end gap-1 border-b border-line px-2 py-1.5">
					<ToggleChip
						variant="button"
						className="h-6.5 px-2 text-xs"
						pressed={wrapJson}
						onPressedChange={(next) => {
							setWrapJson(next);
							onWrap?.(next);
						}}
					>
						<TextWrapIcon aria-hidden="true" />
						Wrap lines
					</ToggleChip>
					{copyButton}
				</div>
			)}
			<pre
				tabIndex={0}
				style={gutter}
				className={
					"m-0 min-h-0 overflow-auto py-3 font-mono text-sm leading-[1.6] text-(--sh-identifier) outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
				}
			>
				<Code
					code={children}
					language={language}
					className={cn(
						lineNumbers ? "code-lines" : "code-plain block px-4 whitespace-normal",
						shouldWrap && "code-wrap",
						!lineNumbers && !shouldWrap && "w-max min-w-full",
					)}
				/>
			</pre>
			{!isJson && copyButton}
		</div>
	);
}

export { Code, CodeBlock, type CodeBlockProps, type CodeProps };
