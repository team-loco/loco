import { CheckIcon, CopyIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";
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
	const [copiedKey, copy] = useCopy(1500);
	const copied = copiedKey === value;

	return (
		<Button
			variant="ghost"
			onClick={() => {
				copy(value, value);
			}}
			title={copied ? "Copied" : (title ?? `Copy ${value}`)}
			className={cn("h-auto gap-1.5 px-1.5 py-0.5", className)}
		>
			{children}
			<span className="flex font-normal text-fg3">
				{copied ? <CheckIcon className="size-[13px]" /> : <CopyIcon className="size-[13px]" />}
			</span>
		</Button>
	);
}
