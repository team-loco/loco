/** The dot for a series with one sample, which a line cannot draw; any other series draws none. */
export function loneSampleDot(samples: number, color: string, opacity = 1) {
	if (samples !== 1) return false;
	return { r: 2.5, fill: color, stroke: color, strokeDasharray: "none", fillOpacity: opacity, strokeOpacity: opacity };
}
