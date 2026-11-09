import { Checkbox as CheckboxBase } from "@/components/ui/checkbox";
import { cn } from "@/lib/utils";

const box =
	"border-line2 bg-raise focus-visible:ring-2 focus-visible:ring-ring dark:bg-raise data-indeterminate:border-primary data-indeterminate:bg-primary data-indeterminate:[&_svg]:hidden data-indeterminate:before:h-[1.5px] data-indeterminate:before:w-2 data-indeterminate:before:rounded-full data-indeterminate:before:bg-primary-foreground";

function Checkbox({ className, ...props }: React.ComponentProps<typeof CheckboxBase>) {
	return <CheckboxBase className={cn(box, className)} {...props} />;
}

export { Checkbox };
