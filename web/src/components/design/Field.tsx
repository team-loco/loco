import { createContext, useId, type ReactNode } from "react";

import { Field as FieldBase, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { cn } from "@/lib/utils";

export const FieldControlContext = createContext<
	{ id: string; describedBy: string | undefined; invalid: boolean } | undefined
>(undefined);

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
	const id = useId();
	const messageId = useId();
	const failed = error !== undefined && error !== null && error !== false;
	const message = failed ? error : hint;
	return (
		<FieldBase className={cn("min-w-0 gap-1.5", className)}>
			<FieldLabel htmlFor={id} className="text-[13px] font-medium text-foreground">
				{label}
			</FieldLabel>
			<FieldControlContext value={{ id, describedBy: message == null ? undefined : messageId, invalid: failed }}>
				{children}
			</FieldControlContext>
			{failed ? (
				<FieldError id={messageId} role={undefined} className="text-[12.5px] text-bad-fg">
					{error}
				</FieldError>
			) : (
				hint != null && (
					<FieldDescription id={messageId} className="m-0 text-[12.5px] text-fg3">
						{hint}
					</FieldDescription>
				)
			)}
		</FieldBase>
	);
}
