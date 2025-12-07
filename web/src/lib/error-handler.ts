import { ConnectError } from "@connectrpc/connect";
import { toast } from "sonner";

export function formatErrorMessage(message: string): string {
	if (!message) return "An error occurred";

	let formatted = message.trim();

	// capitalize first letter
	formatted = formatted.charAt(0).toUpperCase() + formatted.slice(1);

	// add period if not present
	if (!formatted.endsWith(".") && !formatted.endsWith("!") && !formatted.endsWith("?")) {
		formatted += ".";
	}

	return formatted;
}

export function toastConnectError(error: unknown): void {
	if (error instanceof ConnectError) {
		const message = formatErrorMessage(error.rawMessage);
		toast.error(message);
	} else if (error instanceof Error) {
		const message = formatErrorMessage(error.message);
		toast.error(message);
	} else {
		toast.error("An unexpected error occurred.");
	}
}
