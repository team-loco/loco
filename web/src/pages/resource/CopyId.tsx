import { CheckIcon, CopyIcon } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";

import { cn } from "@/lib/utils";

export function CopyId({
	value,
	children,
	className,
	title,
}: {
	value: string;
	children: ReactNode;
	className?: string | undefined;
	title?: string | undefined;
}) {
	const [copied, setCopied] = useState(false);

	useEffect(() => {
		if (!copied) return;
		const t = setTimeout(() => {
			setCopied(false);
		}, 1500);
		return () => {
			clearTimeout(t);
		};
	}, [copied]);

	const copy = () => {
		void navigator.clipboard.writeText(value).then(() => {
			setCopied(true);
		});
	};

	return (
		<button
			type="button"
			onClick={copy}
			title={copied ? "Copied" : (title ?? `Copy ${value}`)}
			className={cn(
				"flex cursor-pointer items-center gap-1.5 rounded-sm border-0 bg-transparent px-1.5 py-0.5 hover:bg-bg3",
				className,
			)}
		>
			{children}
			<span className="flex font-normal text-fg3">
				{copied ? <CheckIcon className="size-[13px]" /> : <CopyIcon className="size-[13px]" />}
			</span>
		</button>
	);
}
