import { Switch as SwitchBase } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

const track =
	"border-0 p-0.5 duration-160 data-[size=default]:h-[18px] data-[size=default]:w-8 data-[size=sm]:h-[15px] data-[size=sm]:w-[26px] data-checked:bg-primary data-unchecked:bg-line2 dark:data-unchecked:bg-line2";

const knob =
	"[&_[data-slot=switch-thumb]]:size-3.5! [&_[data-slot=switch-thumb]]:bg-background! [&_[data-slot=switch-thumb]]:shadow-knob [&_[data-slot=switch-thumb]]:duration-180 [&_[data-slot=switch-thumb]]:ease-[cubic-bezier(0.2,0.8,0.2,1)] dark:[&[data-unchecked]_[data-slot=switch-thumb]]:bg-fg2! [&[data-checked]_[data-slot=switch-thumb]]:translate-x-3.5! [&[data-checked]_[data-slot=switch-thumb]]:bg-primary-foreground! [&[data-size=sm]_[data-slot=switch-thumb]]:size-[11px]! [&[data-size=sm][data-checked]_[data-slot=switch-thumb]]:translate-x-[11px]!";

function Switch({ className, ...props }: React.ComponentProps<typeof SwitchBase>) {
	return <SwitchBase className={cn(track, knob, className)} {...props} />;
}

export { Switch };
