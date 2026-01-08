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

export function getErrorMessage(error: unknown, fallback = "An error occurred"): string {
	if (error instanceof ConnectError) {
		return formatErrorMessage(error.rawMessage || fallback);
	} else if (error instanceof Error) {
		return formatErrorMessage(error.message || fallback);
	}
	return formatErrorMessage(fallback);
}

export function toastConnectError(error: unknown, fallback = "An unexpected error occurred."): void {
	const message = getErrorMessage(error, fallback);
	toast.error(message, {
		duration: 5000,
	});
}
