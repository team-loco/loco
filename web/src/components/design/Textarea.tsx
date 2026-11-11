import { Textarea as TextareaBase } from "@/components/ui/textarea";
import { use } from "react";

import { FieldControlContext } from "@/components/design/Field";
import { cn } from "@/lib/utils";

function Textarea({ className, ...props }: React.ComponentProps<typeof TextareaBase>) {
	const field = use(FieldControlContext);
	return (
		<TextareaBase
			id={field?.id}
			aria-describedby={field?.describedBy}
			aria-invalid={field?.invalid === true || undefined}
			className={cn(
				"field-sizing-fixed min-h-0 resize-y rounded-control px-2.5 py-2 leading-normal disabled:opacity-45 border-line2 bg-raise text-[13.5px] text-foreground placeholder:text-fg4 focus-visible:border-fg3 focus-visible:ring-3 focus-visible:ring-foreground/8 aria-invalid:border-err aria-invalid:ring-0 md:text-[13.5px] [&.font-mono]:text-[12.5px] dark:bg-raise dark:aria-invalid:border-err dark:aria-invalid:ring-0",
				className,
			)}
			{...props}
		/>
	);
}

export { Textarea };
