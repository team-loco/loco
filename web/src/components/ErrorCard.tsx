import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { getErrorMessage } from "@/lib/error-handler";
import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

interface ErrorCardProps {
	error: unknown;
	fallbackMessage?: string;
	minHeight?: string;
}

export function ErrorCard({
	error,
	fallbackMessage = "Failed to load data",
	minHeight = "min-h-96",
}: ErrorCardProps) {
	const [copied, setCopied] = useState(false);
	const errorMessage = getErrorMessage(error, fallbackMessage);
	const match = errorMessage.match(/^(.+?requestId)\s*(.+?)$/i);
	const mainMessage = match ? match[1] : errorMessage;
	const requestId = match ? match[2] : null;

	const handleCopy = () => {
		if (requestId) {
			navigator.clipboard.writeText(requestId);
			setCopied(true);
			toast.success("Request ID copied");
			setTimeout(() => setCopied(false), 2000);
		}
	};

	return (
		<div className={`flex items-center justify-center ${minHeight}`}>
			<Card className="w-full max-w-sm min-w-3xl">
				<CardContent className="p-6 text-center">
					<p className="text-destructive font-heading mb-4 text-2xl">
						Error Loading Data
					</p>
					<div className="text-sm text-foreground opacity-70 whitespace-pre-wrap">
						{mainMessage}
						{requestId && `\n`}
					</div>
					{requestId && (
						<div className="flex items-center justify-center gap-2 mt-4">
							<code className="text-xs text-muted-foreground bg-muted px-2 py-1 rounded font-mono">
								{requestId}
							</code>
							<Button
								size="sm"
								variant="ghost"
								onClick={handleCopy}
								className="h-6 w-6 p-0"
								title="Copy request ID"
							>
								{copied ? (
									<Check className="h-3 w-3" />
								) : (
									<Copy className="h-3 w-3" />
								)}
							</Button>
						</div>
					)}
				</CardContent>
			</Card>
		</div>
	);
}
