import * as React from "react";

import { cn } from "@/lib/utils";

const Input = ({
	ref,
	className,
	type,
	...props
}: React.ComponentProps<"input"> & {
	ref?: React.RefObject<HTMLInputElement | null>;
}) => {
	return (
		<input
			type={type}
			className={cn(
				"flex h-8 w-full rounded-lg border border-input bg-background px-3 py-0.5 text-sm transition-all duration-fast shadow-xs file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:riprimary/30",
				className,
			)}
			ref={ref}
			{...props}
		/>
	);
};
Input.displayName = "Input";

export { Input };
