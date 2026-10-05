import { CheckIcon, CopyIcon } from "lucide-react";
import { useState } from "react";
import { highlight, type LanguageName } from "sugar-high";

import { Button } from "@/components/design/Button";
import { cn } from "@/lib/utils";

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
	filename?: string | undefined;
	className?: string | undefined;
	codeClassName?: string | undefined;
}

function CodeBlock({ children, language, filename, className, codeClassName }: CodeBlockProps) {
	const [copied, setCopied] = useState(false);

	const copy = async () => {
		try {
			await navigator.clipboard.writeText(children);
			setCopied(true);
			setTimeout(() => {
				setCopied(false);
			}, 2000);
		} catch {
			setCopied(false);
		}
	};

	return (
		<div className={cn("overflow-hidden rounded-lg border border-line bg-background", className)}>
			{filename !== undefined && (
				<div className="flex items-center justify-between border-b border-line bg-bg2 py-1.5 pr-2 pl-4">
					<span className="font-mono text-sm font-medium text-fg2">{filename}</span>
					<Button
						variant="ghost"
						size="icon-xs"
						aria-label="Copy code"
						className={copied ? "text-ok-fg" : "text-fg3 hover:text-foreground"}
						onClick={() => {
							void copy();
						}}
					>
						{copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
					</Button>
				</div>
			)}
			<pre className={cn("m-0 overflow-auto p-4 font-mono text-sm leading-[1.6] text-(--sh-identifier)", codeClassName)}>
				<Code code={children} language={language} />
			</pre>
		</div>
	);
}

export { Code, CodeBlock, type CodeBlockProps, type CodeProps };
