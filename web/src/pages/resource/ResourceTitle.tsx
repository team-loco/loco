import { useQuery } from "@connectrpc/connect-query";
import { useParams } from "react-router";
import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { PageTitle } from "@/components/PageTitle";

export function ResourceTitle() {
	const { resourceId = "" } = useParams<{ resourceId: string }>();
	const { data } = useQuery(getResource, { key: { case: "resourceId", value: resourceId } }, { enabled: resourceId !== "" });
	return <PageTitle title={data?.resource?.name ?? "Resource"} />;
}
