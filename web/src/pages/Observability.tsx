import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { AlertCircle } from "lucide-react";

export function Observability() {
	return (
		<div className="space-y-6">
			<Card>
				<CardHeader>
					<CardTitle>Observability</CardTitle>
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
