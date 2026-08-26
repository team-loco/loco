import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/design/Button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/design/Dialog";
import { Label } from "@/components/design/Label";
import { AlertTriangle, Check, Copy } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

 
const BASE_URL: string =
	import.meta.env.VITE_API_URL ?? "http://localhost:8000";

interface TokenDisplayDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	token: string;
}

export function TokenDisplayDialog({
	open,
	onOpenChange,
	token,
}: TokenDisplayDialogProps) {
	const [copied, setCopied] = useState(false);
	const [copiedCurl, setCopiedCurl] = useState(false);

	const handleCopy = async () => {
		try {
			await navigator.clipboard.writeText(token);
			setCopied(true);
			toast.success("Token copied to clipboard");
			setTimeout(() => {
				setCopied(false);
			}, 2000);
		} catch {
			toast.error("Failed to copy token");
		}
	};

	const handleCopyCurl = async () => {
		const curlCommand = `curl -X POST ${BASE_URL}/loco.token.v1.TokenService/GetScopes \\
  -H "Authorization: Bearer ${token}" \\
  -H "Content-Type: application/json" \\
  -d '{}'`;
		try {
			await navigator.clipboard.writeText(curlCommand);
			setCopiedCurl(true);
			toast.success("Command copied to clipboard");
			setTimeout(() => {
				setCopiedCurl(false);
			}, 2000);
		} catch {
			toast.error("Failed to copy command");
		}
	};

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-w-2xl">
				<DialogHeader>
					<DialogTitle className="flex items-center gap-2">
						<Check className="h-5 w-5 text-green-600" />
						Token Created Successfully
					</DialogTitle>
					<DialogDescription>
						Your API token has been created. Make sure to copy it now as you
						won't be able to see it again.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4 py-4">
					{/* Warning Alert */}
					<Alert className="border-orange-500/50 bg-orange-50 dark:bg-orange-950/50">
						<AlertTriangle className="h-4 w-4 text-orange-600 dark:text-orange-400" />
						<AlertDescription className="text-orange-800 dark:text-orange-200">
							<strong>Important:</strong> This token will only be shown once.
							Make sure to copy and store it securely. If you lose it, you'll
							need to create a new token.
						</AlertDescription>
					</Alert>

					{/* Token Display */}
					<div className="space-y-2">
						<Label htmlFor="token-value" className="text-sm font-medium">
							API Token
						</Label>
						<div className="relative p-2 pr-12 bg-muted rounded-lg border border-border">
							<div
								id="token-value"
								className="font-mono text-sm border-border bg-transparent py-0"
							>
								{token}
							</div>
							<Button
								type="button"
								size="icon"
								onClick={() => {
									void handleCopy();
								}}
								className="absolute top-1 right-1 shrink-0"
								variant="ghost"
							>
								{copied ? (
									<Check className="h-4 w-4" />
								) : (
									<Copy className="h-4 w-4" />
								)}
							</Button>
						</div>
						<p className="text-xs text-muted-foreground">
							Use this token in the{" "}
							<code className="bg-muted px-1 py-0.5 rounded">
								Authorization
							</code>{" "}
							header as{" "}
							<code className="bg-muted px-1 py-0.5 rounded">
								Bearer &lt;token&gt;
							</code>
						</p>
					</div>

					{/* Usage Example */}
					<div className="space-y-2">
						<Label className="text-sm font-medium">Example Usage</Label>
						<div className="relative p-4 pr-12 bg-muted rounded-lg border border-border">
							<pre className="text-xs overflow-x-auto">
								<code>{`curl -X POST ${BASE_URL}/loco.token.v1.TokenService/GetScopes \\\n  -H "Authorization: Bearer ${token}" \\\n  -H "Content-Type: application/json" \\\n  -d '{}'`}</code>
							</pre>
							<Button
								type="button"
								size="icon"
								onClick={() => {
									void handleCopyCurl();
								}}
								className="absolute top-2 right-2 shrink-0"
								variant="ghost"
							>
								{copiedCurl ? (
									<Check className="h-4 w-4" />
								) : (
									<Copy className="h-4 w-4" />
								)}
							</Button>
						</div>
					</div>
				</div>

				<DialogFooter>
					<Button
						type="button"
						onClick={() => {
							onOpenChange(false);
						}}
						className="w-full sm:w-auto"
					>
						I've Saved My Token
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
