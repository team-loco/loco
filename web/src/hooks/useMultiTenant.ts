import { useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { DeploymentMode } from "@gen/loco/config/v1/config_pb";

/** Whether this install is the hosted service many organizations share; undefined while the config loads. */
export function useMultiTenant(): boolean | undefined {
	const { data, isPending } = useQuery(getConfig, {});
	if (isPending) return undefined;
	return data?.deploymentMode === DeploymentMode.MULTI_TENANT;
}
