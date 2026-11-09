import { Input as InputBase } from "@/components/ui/input";
import { use } from "react";

import { FieldMessageContext } from "@/components/design/Field";
import { cn } from "@/lib/utils";

function Input({ className, ...props }: React.ComponentProps<typeof InputBase>) {
	const fieldMessage = use(FieldMessageContext);
	return (
		<InputBase
			aria-describedby={fieldMessage}
			className={cn(
				"h-[34px] rounded-lg px-2.5 py-0 disabled:opacity-45 border-line2 bg-raise text-[13.5px] text-foreground placeholder:text-fg4 focus-visible:border-fg3 focus-visible:ring-3 focus-visible:ring-foreground/8 aria-invalid:border-err aria-invalid:ring-0 md:text-[13.5px] [&.font-mono]:text-[12.5px] dark:bg-raise dark:aria-invalid:border-err dark:aria-invalid:ring-0",
				className,
			)}
			{...props}
		/>
	);
}

export { Input };
