import {
	ChartContainer as ChartContainerBase,
	ChartTooltipContent as ChartTooltipContentBase,
} from "@/components/ui/chart";

// Recharts closes its tooltip on mouseout; a page scroll under a still pointer never sends one.
function closeTooltipOnScroll(node: HTMLDivElement | null) {
	if (node === null) return;
	const close = () => {
		(node.querySelector(".recharts-wrapper") ?? node).dispatchEvent(
			new MouseEvent("mouseout", { bubbles: true, relatedTarget: null }),
		);
	};
	window.addEventListener("scroll", close, { capture: true, passive: true });
	return () => {
		window.removeEventListener("scroll", close, { capture: true });
	};
}

function ChartContainer(props: React.ComponentProps<typeof ChartContainerBase>) {
	return <ChartContainerBase ref={closeTooltipOnScroll} {...props} />;
}

function ChartTooltipContent({ payload, ...props }: React.ComponentProps<typeof ChartTooltipContentBase>) {
	return <ChartTooltipContentBase {...props} payload={(payload ?? []).filter((item) => item.type !== "none")} />;
}

export { ChartContainer, ChartTooltipContent };
export { ChartLegend, ChartLegendContent, ChartStyle, ChartTooltip, type ChartConfig } from "@/components/ui/chart";
