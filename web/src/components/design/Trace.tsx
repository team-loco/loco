import type { ComponentProps } from "react";
import { Tabs } from "@base-ui/react/tabs";
import { cn } from "@/lib/utils";
import "./Trace.css";

export function TraceSurface({ className, ...props }: ComponentProps<"section">) {
	return <section className={cn("trace-surface", className)} {...props} />;
}

export function TraceDot({ color }: { color: string }) {
	return <span aria-hidden className="trace-dot" style={{ background: color }} />;
}

export function TraceTabs(props: ComponentProps<typeof Tabs.Root>) {
	return <Tabs.Root {...props} />;
}
export function TraceTabList({ className, ...props }: ComponentProps<typeof Tabs.List>) {
	return <Tabs.List className={cn("trace-attribute-tabs", className)} {...props} />;
}
export function TraceTab(props: ComponentProps<typeof Tabs.Tab>) {
	return <Tabs.Tab className="trace-attribute-tab" {...props} />;
}
export function TraceTabPanel(props: ComponentProps<typeof Tabs.Panel>) {
	return <Tabs.Panel {...props} />;
}
