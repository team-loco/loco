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
	const pendingRef = useRef(new Map<string, string>());
	return {
		...mutation,
		mutateAsync: async (input: MessageInitShape<typeof CreateDeploymentRequestSchema>) => {
			const request = create(CreateDeploymentRequestSchema, input);
			request.requestId = "";
			const key = hashKey([toJson(CreateDeploymentRequestSchema, request)]);
			request.requestId = pendingRef.current.get(key) ?? crypto.randomUUID();
			pendingRef.current.set(key, request.requestId);
			try {
				const response = await mutation.mutateAsync(request);
				pendingRef.current.delete(key);
				return response;
			} catch (error) {
				if (!deploymentOutcomeUnknown(error)) pendingRef.current.delete(key);
				throw error;
			}
		},
	};
}
