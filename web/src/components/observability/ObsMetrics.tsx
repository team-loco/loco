import { ObsMetricChart } from "./ObsMetricChart";

export function ObsMetrics() {
	return (
		<div className="grid grid-cols-1 md:grid-cols-2 gap-4">
			<ObsMetricChart
				title="CPU Utilization"
				metricName="k8s.pod.cpu_request_utilization"
				unit="%"
				aggregation="avg"
				yDomain={[0, 100]}
			/>
			<ObsMetricChart
				title="Memory Utilization"
				metricName="k8s.pod.memory_request_utilization"
				unit="%"
				aggregation="avg"
				yDomain={[0, 100]}
			/>
			<ObsMetricChart
				title="Network I/O"
				metricName="k8s.pod.network.io"
				unit="bytes/s"
				aggregation="avg"
			/>
			<ObsMetricChart
				title="Pod Phase"
				metricName="k8s.pod.phase"
				aggregation="avg"
			/>
		</div>
	);
}
