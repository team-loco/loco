import { cn } from "@/lib/utils";
import { CheckIcon, CopyIcon } from "lucide-react";
import { useEffect, useState } from "react";

// ─── Icon map (filename pattern → simple-icon) ───────────────────────────────
// Lazily imported so the icons only load when this component is rendered.

async function getIconForFilename(filename: string) {
	const icons = await import("@icons-pack/react-simple-icons");
	const map: Record<string, keyof typeof icons> = {
		"*.toml": "SiToml",
		"*.ts": "SiTypescript",
		"*.tsx": "SiReact",
		"*.js": "SiJavascript",
		"*.jsx": "SiReact",
		"*.json": "SiJson",
		"*.go": "SiGo",
		"*.py": "SiPython",
		"*.yaml": "SiYaml",
		"*.yml": "SiYaml",
		"*.md": "SiMarkdown",
		"*.sh": "SiGnubash",
		"*.css": "SiCss",
		"*.html": "SiHtml5",
		"Dockerfile": "SiDocker",
		".env": "SiDotenv",
		"*.rs": "SiRust",
	};

	for (const [pattern, iconKey] of Object.entries(map)) {
		const regex = new RegExp(
			`^${pattern.replace(/\./g, "\\.").replace(/\*/g, ".*")}$`,
		);
		if (regex.test(filename)) {
			const Icon = icons[iconKey] as React.ComponentType<{ className?: string }>;
			return Icon ?? null;
		}
	}
	return null;
}

// ─── Syntax highlighting ──────────────────────────────────────────────────────

async function highlight(code: string, lang: string): Promise<string> {
	const { codeToHtml } = await import("shiki");
	return codeToHtml(code, {
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
	const [FileIcon, setFileIcon] = useState<React.ComponentType<{
		className?: string;
	}> | null>(null);
	const [copied, setCopied] = useState(false);

	// Syntax highlight
	useEffect(() => {
		let cancelled = false;
		highlight(children, language)
			.then((result) => {
				if (!cancelled) setHtml(result);
			})
			.catch(() => {
				// highlight failed — raw code shown via fallback
			});
		return () => {
			cancelled = true;
		};
	}, [children, language]);

	// Icon for filename
	useEffect(() => {
		if (!filename) return;
		getIconForFilename(filename)
			.then((icon) => setFileIcon(() => icon))
			.catch(() => {});
	}, [filename]);

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
			{/* Header */}
			{filename && (
				<div className="flex items-center justify-between px-4 py-2.5 border-b border-[#E7E5E0] bg-[#F7F5F2]">
					<div className="flex items-center gap-2 text-[#57534E]">
						{FileIcon && <FileIcon className="h-4 w-4 shrink-0" />}
						<span className="text-xs font-medium font-mono">{filename}</span>
					</div>
					<button
						onClick={copy}
						className="text-[#78716C] hover:text-[#1C1917] transition-colors p-1 rounded"
						aria-label="Copy code"
					>
						<CopyIconComponent size={14} />
					</button>
				</div>
			)}

			{/* Body */}
			<div
				className={cn(
					"text-sm overflow-x-auto",
					// shiki light/dark theme CSS vars
					"[&_.shiki]:bg-transparent! [&_pre]:bg-transparent! [&_pre]:p-4 [&_pre]:m-0",
					"dark:[&_.shiki_span]:text-[var(--shiki-dark)]!",
					"dark:[&_.shiki]:text-[var(--shiki-dark)]!",
				)}
			>
				{html ? (
					<div
						dangerouslySetInnerHTML={{ __html: html }}
					/>
				) : (
					// Fallback while shiki loads
					<pre className="p-4 m-0 font-mono text-[#1C1917] leading-relaxed">
						<code>{children}</code>
					</pre>
				)}
			</div>
		</div>
	);
}
