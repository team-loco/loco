import { Code, ConnectError } from "@connectrpc/connect";
import { toast } from "sonner";

export function formatErrorMessage(message: string): string {
	if (!message) return "An error occurred";

	let formatted = message.trim();

	// capitalize first letter
	formatted = formatted.charAt(0).toUpperCase() + formatted.slice(1);

	// add period if not present
	if (
		!formatted.endsWith(".") &&
		!formatted.endsWith("!") &&
		!formatted.endsWith("?")
	) {
		formatted += ".";
	}

	return formatted;
}

export function getErrorMessage(
	error: unknown,
	fallback = "An error occurred",
): string {
	if (error instanceof ConnectError) {
		if (error.code === Code.Internal) {
			const requestId = error.metadata.get("x-loco-request-id");
			if (requestId) {
				return `Please reach out to a Loco support engineer and provide this requestId ${requestId}`;
			}
		}
		return formatErrorMessage(error.rawMessage || fallback);
	} else if (error instanceof Error) {
		return formatErrorMessage(error.message || fallback);
	}
	return formatErrorMessage(fallback);
}

export function getRequestIdFromError(error: unknown): string | null {
	if (error instanceof ConnectError && error.code === Code.Internal) {
		return error.metadata.get("x-loco-request-id") || null;
	}
	return null;
}

export function toastConnectError(
	error: unknown,
	fallback = "An unexpected error occurred.",
): void {
	const message = getErrorMessage(error, fallback);
	const requestId = getRequestIdFromError(error);

	if (requestId) {
		toast.error(message, {
			duration: 7000,
			action: {
				label: "Copy ID",
				onClick: () => {
					navigator.clipboard.writeText(requestId);
					toast.success("Request ID copied", { duration: 2000 });
				},
			},
		});
	} else {
		toast.error(message, {
			duration: 5000,
		});
	}
}
