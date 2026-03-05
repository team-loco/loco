import { cn } from "@/lib/utils";
import { CheckIcon, CopyIcon } from "lucide-react";
import { useEffect, useState } from "react";
import type { HighlighterCore } from "shiki/core";

// ─── Singleton highlighter (only toml grammar + 2 themes) ────────────────────

let highlighterPromise: Promise<HighlighterCore> | null = null;

function getHighlighter(): Promise<HighlighterCore> {
	highlighterPromise ??= Promise.all([
		import("shiki/core"),
		import("shiki/engine/javascript"),
		import("shiki/langs/toml.mjs"),
		import("shiki/themes/vitesse-light.mjs"),
		import("shiki/themes/vitesse-dark.mjs"),
	]).then(
		([
			{ createHighlighterCore },
			{ createJavaScriptRegexEngine },
			toml,
			vitesseLight,
			vitesseDark,
		]) =>
			createHighlighterCore({
				themes: [vitesseLight.default, vitesseDark.default],
				langs: [toml.default],
				engine: createJavaScriptRegexEngine(),
			}),
	);
	return highlighterPromise;
}

async function highlight(code: string, lang: string): Promise<string> {
	const h = await getHighlighter();
	return h.codeToHtml(code, {
		lang,
		themes: { light: "vitesse-light", dark: "vitesse-dark" },
	});
}

// ─── Component ────────────────────────────────────────────────────────────────

export interface CodeBlockProps {
	filename?: string;
	language?: string;
	children: string;
	className?: string;
}

export function CodeBlock({
	filename,
	language = "plaintext",
	children,
	className,
}: CodeBlockProps) {
	const [html, setHtml] = useState<string>("");
	const [copied, setCopied] = useState(false);

	useEffect(() => {
		let cancelled = false;
		highlight(children, language)
			.then((result) => {
				if (!cancelled) setHtml(result);
			})
			.catch(() => {});
		return () => {
			cancelled = true;
		};
	}, [children, language]);

	function copy() {
		void navigator.clipboard.writeText(children).then(() => {
			setCopied(true);
			setTimeout(() => setCopied(false), 2000);
		});
	}

	const CopyIconComponent = copied ? CheckIcon : CopyIcon;

	return (
		<div
			className={cn(
				"rounded-xl overflow-hidden border border-[#E7E5E0] bg-white shadow-sm",
				className,
			)}
		>
			{filename && (
				<div className="flex items-center justify-between px-4 py-2.5 border-b border-[#E7E5E0] bg-[#F7F5F2]">
					<span className="text-xs font-medium font-mono text-[#57534E]">
						{filename}
					</span>
					<button
						onClick={copy}
						className="text-[#78716C] hover:text-[#1C1917] transition-colors p-1 rounded"
						aria-label="Copy code"
					>
						<CopyIconComponent size={14} />
					</button>
				</div>
			)}

			<div
				className={cn(
					"text-sm overflow-x-auto",
					"[&_.shiki]:bg-transparent! [&_pre]:bg-transparent! [&_pre]:p-4 [&_pre]:m-0",
					"dark:[&_.shiki_span]:text-[var(--shiki-dark)]!",
					"dark:[&_.shiki]:text-[var(--shiki-dark)]!",
				)}
			>
				{html ? (
					<div dangerouslySetInnerHTML={{ __html: html }} />
				) : (
					<pre className="p-4 m-0 font-mono text-[#1C1917] leading-relaxed">
						<code>{children}</code>
					</pre>
				)}
			</div>
		</div>
	);
}
