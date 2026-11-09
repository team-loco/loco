import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { DomainService } from "@gen/loco/domain/v1/domain_pb";

import { checkTarget, domainCheck, type DomainCheck } from "@/lib/platform-domain";

const AVAILABILITY_DEBOUNCE_MS = 350;
const AVAILABILITY_STALE_MS = 10_000;

export function useDomainAvailability(subdomain: string, platformDomain: string, own: readonly string[]): DomainCheck {
	const transport = useTransport();
	const target = checkTarget(subdomain, platformDomain, own);
	const query = useQuery({
		queryKey: ["domain-availability", target],
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
		retry: false,
	});
	return domainCheck(subdomain, platformDomain, own, {
		fetching: query.isFetching,
		available: query.data,
		error: query.error,
	});
}
