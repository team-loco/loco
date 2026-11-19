import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { DeploymentService } from "@gen/loco/deployment/v1/deployment_pb";

import { getErrorMessage } from "@/lib/error-handler";
import { apiFieldError, imageError } from "@/lib/new-resource";

export const IMAGE_CHECK_KEY = "image-check";
const IMAGE_CHECK_DEBOUNCE_MS = 600;

export interface ImageCheck {
	checking: boolean;
	error: string | null;
}

/** Asks the registry, once the image field settles, whether the image can be pulled. */
export function useImageCheck(workspaceId: string, image: string): ImageCheck {
	const transport = useTransport();
	const trimmed = image.trim();
	const query = useQuery({
		queryKey: [IMAGE_CHECK_KEY, workspaceId, trimmed],
		queryFn: async ({ signal }) => {
			await new Promise((resolve) => setTimeout(resolve, IMAGE_CHECK_DEBOUNCE_MS));
			signal.throwIfAborted();
			await createClient(DeploymentService, transport).resolveImage({ workspaceId, image: trimmed }, { signal });
			return true;
		},
		enabled: imageError(trimmed) === null,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	return {
		checking: query.isFetching,
		error:
			query.error === null
				? null
				: (apiFieldError(query.error)?.message ?? getErrorMessage(query.error, "Couldn't check the image")),
	};
}
