import { Banner } from "@/components/design/Banner";
import { Button } from "@/components/design/Button";
import { getErrorMessage } from "@/lib/error-handler";

export function EventStreamNotice({ error, onRetry }: { error: unknown; onRetry: () => void }) {
	if (error === null) return null;
	return (
		<Banner
			className="mx-0 mt-0"
			tone="warn"
			title="Live updates disconnected"
			action={
				<Button variant="ghost" size="sm" onClick={onRetry}>
					Reconnect
				</Button>
			}
		>
			{getErrorMessage(error, "Couldn’t receive new events.")} Showing the last loaded events.
		</Banner>
	);
}
