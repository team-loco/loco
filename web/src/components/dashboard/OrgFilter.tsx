import { useQuery } from "@connectrpc/connect-query";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";

interface OrgFilterProps {
	selectedOrgId: string | null;
	onOrgChange: (orgId: string) => void;
}

export function OrgFilter({ selectedOrgId, onOrgChange }: OrgFilterProps) {
	const { data: getCurrentUserOrgsRes, isLoading } = useQuery(
		getCurrentUserOrgs,
		{}
	);
	const orgs = getCurrentUserOrgsRes?.orgs ?? [];

	if (isLoading || orgs.length === 0) {
		return null;
	}

	return (
		<Select value={selectedOrgId ?? ""} onValueChange={onOrgChange}>
			<SelectTrigger className="w-full sm:w-48">
				<SelectValue placeholder="Select organization" />
			</SelectTrigger>
			<SelectContent>
				{orgs.map((org) => (
					<SelectItem key={org.id} value={org.id}>
						{org.name}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}
