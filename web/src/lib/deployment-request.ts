import { Code, ConnectError } from "@connectrpc/connect";

/** A transport failure can arrive after the server committed the deployment. */
export function deploymentOutcomeUnknown(error: unknown): boolean {
	if (!(error instanceof ConnectError)) return true;
	return [Code.Unknown, Code.Canceled, Code.DeadlineExceeded, Code.Unavailable, Code.Internal, Code.DataLoss].includes(
		error.code,
	);
}
