import { Code, ConnectError } from "@connectrpc/connect";
import { toast } from "sonner";

import { CopyId } from "@/components/design/CopyId";

function formatErrorMessage(message: string): string {
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
		if (error.code === Code.Internal) {
			const id = requestId(error);
			if (id !== null) {
				return `Please reach out to a Loco support engineer and provide this requestId ${id}`;
			}
		}
		return formatErrorMessage(error.rawMessage || fallback);
	} else if (error instanceof Error) {
		return formatErrorMessage(error.message || fallback);
	}
	return formatErrorMessage(fallback);
}

export function isUnimplemented(error: unknown): boolean {
	return error instanceof ConnectError && error.code === Code.Unimplemented;
}

export function requestId(error: unknown): string | null {
	return error instanceof ConnectError ? (error.metadata.get("x-loco-request-id") ?? null) : null;
}

export type ErrorKind = "unavailable" | "internal" | "denied" | "rate";

/** The full-page state a failed load shows; null for Unauthenticated, which redirects to sign-in instead. */
export function errorKind(error: unknown): ErrorKind | null {
	if (!(error instanceof ConnectError)) return "internal";
	if (error.code === Code.Unauthenticated) return null;
	if (error.code === Code.PermissionDenied) return "denied";
	if (error.code === Code.ResourceExhausted) return "rate";
	if (error.code === Code.Unavailable || error.code === Code.DeadlineExceeded) return "unavailable";
	// connect-web reports a fetch that got no response, such as the API being down, as Unknown
	// wrapping the browser's TypeError.
	if (error.code === Code.Unknown && error.cause instanceof TypeError) return "unavailable";
	return "internal";
}

export function RequestIdChip({ id }: { id: string }) {
	return (
		<CopyId
			value={id}
			title="Copy request ID"
			className="mt-[3px] max-w-full rounded-[6px] border border-line bg-panel px-1.5 py-0.5 font-mono text-[10.5px] tracking-[-0.01em] text-fg2 hover:border-fg3 hover:bg-panel hover:text-foreground [&_svg]:size-3!"
		>
			<span className="min-w-0 truncate">{id}</span>
		</CopyId>
	);
}

export function toastConnectError(error: unknown, fallback = "An unexpected error occurred."): void {
	const id = error instanceof ConnectError && error.code === Code.Internal ? requestId(error) : null;
	if (id === null) {
		toast.error(getErrorMessage(error, fallback), { duration: 5000 });
		return;
	}
	toast.error(fallback, {
		duration: 7000,
		description: (
			<span className="flex flex-col items-start">
				Something failed on Loco’s side. Send support this request ID.
				<RequestIdChip id={id} />
			</span>
		),
	});
}
