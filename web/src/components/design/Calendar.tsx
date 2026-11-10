import { Calendar as CalendarBase } from "@/components/ui/calendar";
import { cn } from "@/lib/utils";

export function Calendar({ className, ...props }: React.ComponentProps<typeof CalendarBase>) {
	return <CalendarBase className={cn("p-0 text-[13px] [--cell-size:--spacing(8)]", className)} {...props} />;
}
