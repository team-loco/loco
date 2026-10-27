import { useQuery } from "@connectrpc/connect-query";
import { getOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { useParams } from "react-router";

import { Banner } from "@/components/design/Banner";
import { useMyScopes } from "@/hooks/useMyScopes";

import { suspensionReason } from "./suspension";

const SUSPENSION_STALE_MS = 60_000;

export function SuspensionBanner() {
	const { orgId = "" } = useParams();
	const scopes = useMyScopes();
	const canReadOrg = orgId !== "" && !scopes.isLoading && scopes.orgMember(orgId);
	const { data } = useQuery(
		getOrg,
		{ key: { case: "orgId", value: orgId } },
		{ enabled: canReadOrg, staleTime: SUSPENSION_STALE_MS },
	);
	const reason = suspensionReason(data?.organization);
	if (reason === null) return null;
	return (
		<Banner title="This organization is suspended.">
			Its applications are stopped and deployments are refused until it is resumed. Reason: {reason}
		</Banner>
	);
}
