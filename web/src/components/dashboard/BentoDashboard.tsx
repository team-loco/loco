import { Card } from "@/components/ui/card";
import type { Resource } from "@/gen/loco/resource/v1/resource_pb";
import { Activity, Box, Cpu, GitBranch, Terminal } from "lucide-react";
import { Line, LineChart, ResponsiveContainer, Tooltip } from "recharts";

interface BentoDashboardProps {
	resources: Resource[];
	workspaceId?: string;
}

// Generate mock data for the hero chart
const chartData = Array.from({ length: 24 }).map((_, i) => ({
	time: `${i}:00`,
	cpu: Math.floor(Math.random() * 40) + 10,
	memory: Math.floor(Math.random() * 30) + 50,
}));

export function BentoDashboard({ resources }: BentoDashboardProps) {
	const activeResources = resources.filter((r) => r.status !== 3).length;

	return (
		<div className="grid grid-cols-1 md:grid-cols-4 lg:grid-cols-12 gap-4 lg:gap-6 auto-rows-[minmax(120px,auto)]">
			<Card className="col-span-1 md:col-span-4 lg:col-span-8 lg:row-span-3 border-border rounded-2xl shadow-sm overflow-hidden flex flex-col relative group py-0">
				<div className="p-6 border-b border-border/50 flex justify-between items-center z-10">
					<div>
						<h2 className="text-xl font-bold font-mono tracking-tight text-foreground flex items-center gap-2">
							<Box className="w-5 h-5 text-primary" />
							cluster::production
						</h2>
						<p className="text-sm text-muted-foreground mt-1">
							Live telemetry across {resources.length} resources
						</p>
					</div>
					<div className="flex items-center gap-4">
						<div className="flex flex-col items-end">
							<span className="text-2xl font-bold text-emerald-600 dark:text-emerald-400">
								99.9%
							</span>
							<span className="text-xs uppercase tracking-wider text-muted-foreground font-semibold">
								Uptime
							</span>
						</div>
					</div>
				</div>

				{/* Hero Chart Area */}
				<div className="flex-1 p-6 relative z-10 w-full min-h-[250px]">
					<ResponsiveContainer width="100%" height="100%">
						<LineChart data={chartData}>
							<Tooltip
								contentStyle={{
									backgroundColor: "var(--card)",
									borderColor: "var(--border)",
									borderRadius: "8px",
								}}
								itemStyle={{ color: "var(--foreground)" }}
							/>
							<Line
								type="monotone"
								dataKey="cpu"
								stroke="#C7654F"
								strokeWidth={3}
								dot={false}
								activeDot={{
									r: 6,
									fill: "#C7654F",
									stroke: "var(--background)",
									strokeWidth: 2,
								}}
							/>
							<Line
								type="monotone"
								dataKey="memory"
								stroke="#d97706"
								strokeWidth={3}
								dot={false}
								activeDot={{
									r: 6,
									fill: "#d97706",
									stroke: "var(--background)",
									strokeWidth: 2,
								}}
							/>
						</LineChart>
					</ResponsiveContainer>
				</div>
			</Card>

			{/* WIDGET 2: Active Deployments Feed (col-span-4, row-span-4) */}
			<Card className="col-span-1 md:col-span-4 lg:col-span-4 lg:row-span-4 border-border rounded-2xl shadow-sm flex flex-col overflow-hidden py-0">
				<div className="p-5 border-b border-border/50">
					<h3 className="font-bold text-foreground flex items-center gap-2">
						<Terminal className="w-4 h-4" /> Activity Stream
					</h3>
				</div>
				<div className="flex-1 overflow-y-auto p-5 space-y-4">
					{/* Mock feed items */}
					{[...Array(5)].map((_, i) => (
						<div
							key={i}
							className="flex gap-3 relative before:absolute before:left-2.5 before:top-6 before:-bottom-4 before:w-px before:bg-border last:before:hidden"
						>
							<div className="w-5 h-5 rounded-full bg-primary/10 border border-primary/20 flex items-center justify-center shrink-0 mt-0.5 z-10">
								<div className="w-1.5 h-1.5 rounded-full bg-primary" />
							</div>
							<div className="pb-2">
								<p className="text-sm font-medium text-foreground">
									<code className="text-xs bg-muted px-1 py-0.5 rounded font-mono text-primary mr-1 border border-border">
										api-gateway
									</code>
									deployed successfully
								</p>
								<div className="flex items-center gap-2 mt-1 text-xs text-muted-foreground">
									<span className="flex items-center gap-1">
										<GitBranch className="w-3 h-3" /> main
									</span>
									<span>•</span>
									<span>2m ago</span>
									<span>•</span>
									<span className="font-mono">#b719f10</span>
								</div>
							</div>
						</div>
					))}
				</div>
			</Card>

			{/* WIDGET 3: Resources Roster (col-span-8, row-span-2) */}
			<Card className="col-span-1 md:col-span-4 lg:col-span-8 lg:row-span-2 border-border rounded-2xl shadow-sm flex flex-col overflow-hidden py-0">
				<div className="p-5 border-b border-border/50 flex justify-between items-center">
					<h3 className="font-bold text-foreground">Resources</h3>
					<span className="text-xs font-mono bg-muted px-2 py-1 rounded-md text-muted-foreground border border-border">
						{activeResources} Active
					</span>
				</div>
				<div className="grid grid-cols-2 lg:grid-cols-4 gap-px bg-border/50 p-px">
					{resources.slice(0, 8).map((resource) => (
						<div
							key={resource.id.toString()}
							className="bg-card hover:bg-muted/30 p-4 transition-colors cursor-pointer flex flex-col justify-between min-h-[100px]"
						>
							<div className="flex items-start justify-between">
								<h4 className="font-semibold text-sm truncate pr-2">
									{resource.name}
								</h4>
								<div className="w-2 h-2 rounded-full bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.4)] shrink-0 mt-1.5" />
							</div>
							<div className="flex items-center justify-between mt-4">
								<div className="flex items-center gap-1 text-xs text-muted-foreground font-mono">
									<Cpu className="w-3 h-3" />
									{Math.floor(Math.random() * 40)}%
								</div>
								<span className="text-xs text-muted-foreground">Prod</span>
							</div>
						</div>
					))}
					{resources.length === 0 && (
						<div className="col-span-full p-8 text-center text-muted-foreground bg-card">
							No resources provisioned
						</div>
					)}
				</div>
			</Card>

			{/* WIDGET 4: Quick Cost/Traffic metrics (col-span-4, row-span-1) */}
			<Card className="col-span-1 md:col-span-4 lg:col-span-4 lg:row-span-1 border-border rounded-2xl shadow-sm p-5 flex flex-col justify-center relative overflow-hidden">
				<h3 className="text-sm font-medium text-muted-foreground flex items-center gap-1.5">
					<Activity className="w-4 h-4" /> Global Traffic
				</h3>
				<div className="mt-2 flex items-baseline gap-2">
					<span className="text-3xl font-bold tracking-tight">2.4M</span>
					<span className="text-sm text-muted-foreground">reqs/mo</span>
				</div>
			</Card>
		</div>
	);
}
