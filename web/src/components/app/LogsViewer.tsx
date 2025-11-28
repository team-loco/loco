import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useStreamLogs } from "@/hooks/useStreamLogs";
import { Search, RefreshCw, Download, Pause, Play } from "lucide-react";
import { useState, useRef, useEffect } from "react";

interface LogsViewerProps {
	appId: string;
	isLoading?: boolean;
}

export function LogsViewer({ appId, isLoading = false }: LogsViewerProps) {
	const { logs } = useStreamLogs(appId);
	const [searchTerm, setSearchTerm] = useState("");
	const [isFollowing, setIsFollowing] = useState(true);
	const logsEndRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		if (isFollowing) {
			logsEndRef.current?.scrollIntoView({ behavior: "smooth" });
		}
	}, [logs, isFollowing]);

	const filteredLogs = logs.filter((log) =>
		log.message.toLowerCase().includes(searchTerm.toLowerCase())
	);

	const getLogColor = (level: string): string => {
		switch (level) {
			case "ERROR":
				return "text-red-600";
			case "WARN":
				return "text-yellow-600";
			case "DEBUG":
				return "text-blue-600";
			default:
				return "text-foreground";
		}
	};

	const handleExport = () => {
		const logsText = logs
			.map((log) => `[${log.timestamp}] ${log.level} ${log.message}`)
			.join("\n");
		const element = document.createElement("a");
		element.setAttribute(
			"href",
			"data:text/plain;charset=utf-8," + encodeURIComponent(logsText)
		);
		element.setAttribute("download", `logs-${appId}-${Date.now()}.txt`);
		element.style.display = "none";
		document.body.appendChild(element);
		element.click();
		document.body.removeChild(element);
	};

	if (isLoading) {
		return (
			<Card className="animate-pulse">
				<CardContent className="p-6">
					<div className="h-6 bg-main/20 rounded w-1/4"></div>
				</CardContent>
			</Card>
		);
	}

	return (
		<Card className="border-2">
			<CardHeader>
				<CardTitle>Logs</CardTitle>
			</CardHeader>
			<CardContent className="space-y-4">
				{/* Toolbar */}
				<div className="flex flex-col sm:flex-row gap-2 items-start sm:items-center">
					<div className="relative flex-1 w-full">
						<Search className="absolute left-3 top-2.5 h-4 w-4 text-foreground opacity-50" />
						<Input
							placeholder="Search logs..."
							value={searchTerm}
							onChange={(e) => setSearchTerm(e.target.value)}
							className="pl-9"
						/>
					</div>

					<div className="flex gap-2 w-full sm:w-auto">
						<Button
							variant="noShadow"
							size="sm"
							onClick={() => setIsFollowing(!isFollowing)}
							className="border-2 flex-1 sm:flex-none"
							title={isFollowing ? "Stop following" : "Follow logs"}
						>
							{isFollowing ? (
								<Pause className="w-4 h-4" />
							) : (
								<Play className="w-4 h-4" />
							)}
						</Button>
						<Button
							variant="noShadow"
							size="sm"
							className="border-2 flex-1 sm:flex-none"
						>
							<RefreshCw className="w-4 h-4" />
						</Button>
						<Button
							variant="noShadow"
							size="sm"
							onClick={handleExport}
							className="border-2 flex-1 sm:flex-none"
						>
							<Download className="w-4 h-4" />
						</Button>
					</div>
				</div>

				{/* Logs Display */}
				<div className="bg-foreground/5 border-2 border-border rounded-neo p-4 font-mono text-xs max-h-96 overflow-y-auto">
					{filteredLogs.length > 0 ? (
						<div className="space-y-1">
							{filteredLogs.map((log, index) => (
								<div
									key={index}
									className={`flex gap-2 ${getLogColor(log.level)}`}
								>
									<span className="text-foreground opacity-50 flex-shrink-0">
										{new Date(log.timestamp).toLocaleTimeString()}
									</span>
									<span className="flex-shrink-0 w-8">
										[{log.level}]
									</span>
									<span className="text-foreground opacity-80 break-all">
										{log.message}
									</span>
								</div>
							))}
							<div ref={logsEndRef} />
						</div>
					) : (
						<p className="text-foreground opacity-50">
							{logs.length === 0
								? "No logs yet"
								: `No logs match "${searchTerm}"`}
						</p>
					)}
				</div>
			</CardContent>
		</Card>
	);
}
