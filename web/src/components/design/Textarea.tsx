import { Textarea as TextareaBase } from "@/components/ui/textarea";
import { Field as FieldPrimitive } from "@base-ui/react/field";

import { cn } from "@/lib/utils";

function Textarea({ className, ...props }: React.ComponentProps<typeof TextareaBase>) {
	return (
		<FieldPrimitive.Control
			render={<TextareaBase {...props} />}
			value={props.value}
			defaultValue={props.defaultValue}
			disabled={props.disabled}
			name={props.name}
			id={props.id}
			className={cn(
				"field-sizing-fixed min-h-0 resize-y rounded-control px-2.5 py-2 leading-normal disabled:opacity-45 border-line2 bg-raise text-[13.5px] text-foreground placeholder:text-fg4 focus-visible:border-fg3 focus-visible:ring-3 focus-visible:ring-foreground/8 aria-invalid:border-err aria-invalid:ring-0 md:text-[13.5px] [&.font-mono]:text-[12.5px] dark:bg-raise dark:aria-invalid:border-err dark:aria-invalid:ring-0",
				className,
			)}
		/>
	);
}

export { Textarea };
