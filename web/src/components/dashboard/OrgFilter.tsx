import { useAuth } from "@/auth/AuthProvider";
import { DropdownSelector } from "@/components/ui/dropdown-selector";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { useQuery } from "@connectrpc/connect-query";
import { Building2 } from "lucide-react";

interface OrgFilterProps {
	selectedOrgId: string | null;
	onOrgChange: (orgId: string) => void;
}

export function OrgFilter({ selectedOrgId, onOrgChange }: OrgFilterProps) {
	const { user } = useAuth();
	const { data: listUserOrgsRes, isLoading } = useQuery(
		listUserOrgs,
		user ? { userId: user.id } : undefined,
		{ enabled: !!user },
	);
	const orgs = listUserOrgsRes?.orgs ?? [];

	if (isLoading || orgs.length === 0) {
		return null;
	}

	const selectedOrg = orgs.find((org) => org.id === selectedOrgId);

	return (
		<DropdownSelector
			label={selectedOrg?.name ?? "Select organization"}
			icon={Building2}
			items={orgs.map((org) => ({
				value: org.id,
				label: org.name,
			}))}
			onSelect={(value) => { onOrgChange(value as string); }}
			contentWidth="w-56"
		/>
	);
}
