import { Spinner as SpinnerBase } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";

export function Spinner({ className, ...props }: React.ComponentProps<typeof SpinnerBase>) {
	return <SpinnerBase className={cn("motion-reduce:animate-none", className)} {...props} />;
}
