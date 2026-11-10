import { Field as FieldPrimitive } from "@base-ui/react/field";
import type { ReactNode } from "react";
import { Field as FieldBase, FieldLabel, FieldDescription, FieldError } from "@/components/ui/field";
import { cn } from "@/lib/utils";

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
	const failed = error !== undefined && error !== null && error !== false;
	return (
		<FieldPrimitive.Root invalid={failed} render={<FieldBase />} className={cn("min-w-0 gap-1.5", className)}>
			<FieldPrimitive.Label render={<FieldLabel />} className="text-[13px] font-medium text-foreground">
				{label}
			</FieldPrimitive.Label>
			{children}
			{failed ? (
				<FieldPrimitive.Error match render={<FieldError role={undefined} />} className="text-[12.5px] text-bad-fg">
					{error}
				</FieldPrimitive.Error>
			) : (
				hint != null && (
					<FieldPrimitive.Description render={<FieldDescription />} className="m-0 text-[12.5px] text-fg3">
						{hint}
					</FieldPrimitive.Description>
				)
			)}
		</FieldPrimitive.Root>
	);
}
