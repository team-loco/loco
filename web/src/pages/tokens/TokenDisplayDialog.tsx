import { useState } from "react";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Check, Copy, AlertTriangle } from "lucide-react";
import { toast } from "sonner";

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

	const handleCopy = async () => {
		try {
			await navigator.clipboard.writeText(token);
			setCopied(true);
			toast.success("Token copied to clipboard");
			setTimeout(() => setCopied(false), 2000);
		} catch (error) {
			toast.error("Failed to copy token");
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
						Your API token has been created. Make sure to copy it now as you won't
						be able to see it again.
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4 py-4">
					{/* Warning Alert */}
					<Alert className="border-orange-500/50 bg-orange-50 dark:bg-orange-950/50">
						<AlertTriangle className="h-4 w-4 text-orange-600 dark:text-orange-400" />
						<AlertDescription className="text-orange-800 dark:text-orange-200">
							<strong>Important:</strong> This token will only be shown once. Make
							sure to copy and store it securely. If you lose it, you'll need to
							create a new token.
						</AlertDescription>
					</Alert>

					{/* Token Display */}
					<div className="space-y-2">
						<Label htmlFor="token-value" className="text-sm font-medium">
							API Token
						</Label>
						<div className="flex gap-2">
							<Input
								id="token-value"
								value={token}
								readOnly
								className="font-mono text-sm border-border bg-muted"
								onClick={(e) => e.currentTarget.select()}
							/>
							<Button
								type="button"
								variant={copied ? "default" : "outline"}
								size="icon"
								onClick={handleCopy}
								className="shrink-0"
							>
								{copied ? (
									<Check className="h-4 w-4" />
								) : (
									<Copy className="h-4 w-4" />
								)}
							</Button>
						</div>
						<p className="text-xs text-muted-foreground">
							Use this token in the <code className="bg-muted px-1 py-0.5 rounded">Authorization</code> header as{" "}
							<code className="bg-muted px-1 py-0.5 rounded">Bearer &lt;token&gt;</code>
						</p>
					</div>

					{/* Usage Example */}
					<div className="space-y-2">
						<Label className="text-sm font-medium">Example Usage</Label>
						<div className="p-4 bg-muted rounded-lg border border-border">
							<pre className="text-xs overflow-x-auto">
								<code>{`curl -H "Authorization: Bearer ${token.substring(0, 20)}..." \\
  https://api.loco.dev/v1/resources`}</code>
							</pre>
						</div>
					</div>
				</div>

				<DialogFooter>
					<Button
						type="button"
						onClick={() => onOpenChange(false)}
						className="w-full sm:w-auto"
					>
						I've Saved My Token
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
