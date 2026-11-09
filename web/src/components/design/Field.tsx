import { createContext, useId, type ReactNode } from "react";

import { cn } from "@/lib/utils";

export const FieldMessageContext = createContext<string | undefined>(undefined);

export function Field({
	label,
	hint,
	error,
	className,
	children,
}: {
	label: ReactNode;
	hint?: ReactNode;
	error?: ReactNode;
	className?: string;
	children: ReactNode;
}) {
	const messageId = useId();
	const failed = error !== undefined && error !== null && error !== false;
	const message = failed ? error : hint;
	return (
		<div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
			<label className="flex min-w-0 flex-col gap-1.5">
				<span className="text-[13px] font-medium text-foreground">{label}</span>
				<FieldMessageContext value={message === undefined ? undefined : messageId}>{children}</FieldMessageContext>
			</label>
			{message !== undefined && (
				<span id={messageId} className={cn("text-[12.5px]", failed ? "text-bad-fg" : "text-fg3")}>
					{message}
				</span>
			)}
		</div>
	);
}
