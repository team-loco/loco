/**
 * Allowed CPU and memory values for a resource's service spec.
 *
 * Declared at module scope so the arrays keep a stable identity across renders —
 * the sliders and `useMemo` calls in ScaleCard/DeploymentStatusCard index into
 * them, and recreating them per render would defeat memoization.
 */
export const CPU_OPTIONS: string[] = [
	"100m",
	"250m",
	"500m",
	"750m",
	"1000m",
	"1250m",
	"1500m",
	"1750m",
	"2000m",
];

export const MEMORY_OPTIONS: string[] = [
	"256Mi",
	"512Mi",
	"768Mi",
	"1Gi",
	"1.25Gi",
	"1.5Gi",
	"2Gi",
];
