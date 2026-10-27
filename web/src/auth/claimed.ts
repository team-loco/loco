import type { ClaimedOrg } from "@gen/loco/user/v1/user_pb";

let claimed: ClaimedOrg[] = [];
const listeners = new Set<() => void>();

export function subscribeClaimedOrgs(listener: () => void): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}

export function getClaimedOrgs(): ClaimedOrg[] {
	return claimed;
}

export function setClaimedOrgs(next: ClaimedOrg[]): void {
	claimed = next;
	for (const listener of listeners) listener();
}
