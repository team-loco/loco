import { Link } from "react-router";

import { cn } from "@/lib/utils";

/** A link whose hit area covers its whole table row; controls in the row stay on top. */
export function RowLink({ className, ...props }: React.ComponentProps<typeof Link>) {
	return (
		<Link
			data-row-link=""
			className={cn(
				"underline-offset-3 group-hover/row:underline after:absolute after:inset-0 after:content-['']",
				className,
			)}
			{...props}
		/>
	);
}
