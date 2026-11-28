import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { QueryObserver, queryOptions, useQuery, type QueryClient } from "@tanstack/react-query";
import { DeploymentService } from "@gen/loco/deployment/v1/deployment_pb";

import { getErrorMessage } from "@/lib/error-handler";
import { apiFieldError, imageError } from "@/lib/new-resource";

const IMAGE_CHECK_DEBOUNCE_MS = 600;

export interface ImageCheck {
	checking: boolean;
	error: string | null;
}

export function imageResolutionOptions(transport: Transport, workspaceId: string, image: string) {
	const input = { workspaceId, image: image.trim() };
	return queryOptions({
		queryKey: createConnectQueryKey({
			schema: DeploymentService.method.resolveImage,
			transport,
			cardinality: "finite",
			input,
		}),
		queryFn: async ({ signal }) => {
			await new Promise((resolve) => setTimeout(resolve, IMAGE_CHECK_DEBOUNCE_MS));
			signal.throwIfAborted();
			return await createClient(DeploymentService, transport).resolveImage(input, { signal });
		},
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
}

/** A pending deployment owns the check even when the image field switches queries. */
export async function resolveDraftImage(client: QueryClient, transport: Transport, workspaceId: string, image: string) {
	const options = imageResolutionOptions(transport, workspaceId, image);
	const observer = new QueryObserver(client, options);
	const unsubscribe = observer.subscribe(() => {});
	try {
		return await client.query(options);
	} finally {
		unsubscribe();
	}
}

/** Asks the registry, once the image field settles, whether the image can be pulled. */
export function useImageCheck(workspaceId: string, image: string): ImageCheck {
	const query = useQuery({
		...imageResolutionOptions(useTransport(), workspaceId, image),
		enabled: imageError(image.trim()) === null,
	});
	return {
		checking: query.isFetching,
		error:
			query.error === null
				? null
				: (apiFieldError(query.error)?.message ?? getErrorMessage(query.error, "Couldn't check the image")),
	};
}
