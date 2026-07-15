import { Card, CardContent, CardHeader, CardTitle } from "@/components/design/Card";
import { AlertCircle } from "lucide-react";

export function Usage() {
	return (
		<div className="w-full flex justify-center">
			<Card className="w-[95%]">
				<CardHeader>
					<CardTitle>Usage</CardTitle>
				</CardHeader>
				<CardContent>
					<div className="flex flex-col items-center justify-center py-12">
						<AlertCircle className="h-12 w-12 text-muted-foreground mb-3" />
						<p className="text-muted-foreground">Coming soon</p>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
