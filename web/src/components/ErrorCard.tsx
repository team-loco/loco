import { AlertTriangle, Check, Copy } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";
import { getErrorMessage } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

interface ErrorCardProps {
	error: unknown;
	fallbackMessage?: string | undefined;
	minHeight?: string | undefined;
}

export function ErrorCard({ error, fallbackMessage = "Failed to load data", minHeight = "min-h-96" }: ErrorCardProps) {
	const [copiedKey, copy] = useCopy(2000);
	const copied = copiedKey !== null;
	const errorMessage = getErrorMessage(error, fallbackMessage);
	const match = /^(.+?requestId)\s*(.+?)$/i.exec(errorMessage);
	const mainMessage = match?.[1] ?? errorMessage;
	const requestId = match?.[2] ?? null;

	const handleCopy = () => {
		if (requestId !== null) {
			copy("request", requestId);
			toast.success("Request ID copied");
		}
	};

	return (
		<div className={cn("flex items-center justify-center px-4", minHeight)}>
			<div className="flex w-full max-w-md flex-col items-center gap-2 rounded-lg border border-line bg-background px-6 py-8 text-center">
				<span className="mb-1 flex size-10 items-center justify-center rounded-xl bg-bad-bg text-bad-fg">
					<AlertTriangle className="size-[18px]" />
				</span>
				<span className="text-lg font-semibold">Error loading data</span>
				<p className="m-0 whitespace-pre-wrap text-fg3">{mainMessage}</p>
				{requestId !== null && (
					<div className="mt-2 flex items-center gap-1.5">
						<code className="rounded-xs bg-bg3 px-2 py-0.5 font-mono text-sm text-fg2">{requestId}</code>
						<Button size="icon-xs" variant="ghost" onClick={handleCopy} title="Copy request ID" aria-label="Copy request ID">
							{copied ? <Check className="size-3" /> : <Copy className="size-3" />}
						</Button>
					</div>
				)}
			</div>
		</div>
	);
}
