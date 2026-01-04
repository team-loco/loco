import { useQuery } from "@connectrpc/connect-query";
import { listUserOrgs } from "@/gen/org/v1";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";

interface OrgFilterProps {
	selectedOrgId: bigint | null;
	onOrgChange: (orgId: bigint) => void;
}

export function OrgFilter({ selectedOrgId, onOrgChange }: OrgFilterProps) {
	const { data: listUserOrgsRes, isLoading } = useQuery(
		listUserOrgs,
		{ userId: 0n }
	);
	const orgs = listUserOrgsRes?.orgs ?? [];

	if (isLoading || orgs.length === 0) {
		return null;
	}

	return (
		<Select value={selectedOrgId?.toString() ?? ""} onValueChange={(value) => onOrgChange(BigInt(value))}>
			<SelectTrigger className="w-full sm:w-48">
				<SelectValue placeholder="Select organization" />
			</SelectTrigger>
			<SelectContent>
				{orgs.map((org) => (
					<SelectItem key={org.id} value={org.id.toString()}>
						{org.name}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}
