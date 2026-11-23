import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { DomainService } from "@gen/loco/domain/v1/domain_pb";

import { domainCheck, type DomainCheck } from "@/lib/app-domain";

const AVAILABILITY_DEBOUNCE_MS = 350;
const AVAILABILITY_STALE_MS = 10_000;
export const DOMAIN_AVAILABILITY_KEY = "domain-availability";

export function useDomainAvailability(subdomain: string, appDomain: string, own: readonly string[]): DomainCheck {
	const transport = useTransport();
	const before = domainCheck(subdomain, appDomain, own, { fetching: false, available: undefined, error: null });
	const target = before.state === "checking" ? before.host : null;
	const query = useQuery({
		queryKey: [DOMAIN_AVAILABILITY_KEY, target],
		queryFn: async ({ signal }) => {
			await new Promise((resolve) => setTimeout(resolve, AVAILABILITY_DEBOUNCE_MS));
			signal.throwIfAborted();
			const res = await createClient(DomainService, transport).checkDomainAvailability(
				{ domain: target ?? "" },
				{ signal },
			);
			return res.isAvailable;
		},
		enabled: target !== null,
		staleTime: AVAILABILITY_STALE_MS,
		refetchOnMount: "always",
		retry: false,
	});
	return domainCheck(subdomain, appDomain, own, {
		fetching: query.isFetching,
		available: query.data,
		error: query.error,
	});
}
