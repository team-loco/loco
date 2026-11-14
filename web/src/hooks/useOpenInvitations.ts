import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";

import { invitationsOptions } from "@/hooks/completeMembers";
import { tsMs } from "@/lib/time";

/** Invitations not yet expired at nowMs, which count against the organization's invitation limit; undefined until loaded. */
export function useOpenInvitations(orgId: string, nowMs: number): number | undefined {
	const { data } = useQuery(invitationsOptions(useTransport(), orgId));
	return data?.invitations.filter((i) => tsMs(i.expiresAt) > nowMs).length;
}
