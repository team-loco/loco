import { create, toJson, type MessageInitShape } from "@bufbuild/protobuf";
import { useMutation } from "@connectrpc/connect-query";
import { hashKey } from "@tanstack/react-query";
import { useRef } from "react";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { CreateDeploymentRequestSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentOutcomeUnknown } from "@/lib/deployment-request";

/** An ambiguous reply keeps the operation's ID for a retry of the same input. */
export function useCreateDeployment() {
	const mutation = useMutation(createDeployment);
	const pendingRef = useRef<{ key: string; id: string } | null>(null);
	const finish = (id: string) => {
		if (pendingRef.current?.id === id) pendingRef.current = null;
	};
	return {
		...mutation,
		mutateAsync: async (input: MessageInitShape<typeof CreateDeploymentRequestSchema>) => {
			if (input.requestId) return await mutation.mutateAsync(input);
			const request = create(CreateDeploymentRequestSchema, input);
			request.requestId = "";
			const key = hashKey([toJson(CreateDeploymentRequestSchema, request)]);
			if (pendingRef.current?.key !== key) pendingRef.current = { key, id: crypto.randomUUID() };
			request.requestId = pendingRef.current.id;
			try {
				const response = await mutation.mutateAsync(request);
				finish(request.requestId);
				return response;
			} catch (error) {
				if (!deploymentOutcomeUnknown(error)) finish(request.requestId);
				throw error;
			}
		},
	};
}
