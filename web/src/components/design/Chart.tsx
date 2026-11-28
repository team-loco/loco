import { useTheme } from "@/lib/use-theme";
import { ChartContainer as ChartContainerBase } from "@/components/ui/chart";

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
	const { color } = useTheme();
	return <ChartContainerBase key={color} ref={closeTooltipOnScroll} {...props} />;
}

export { ChartContainer };
export {
	ChartLegend,
	ChartLegendContent,
	ChartStyle,
	ChartTooltip,
	ChartTooltipContent,
	type ChartConfig,
} from "@/components/ui/chart";
