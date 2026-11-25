import { Badge } from "@/components/design/Badge";

/** Marks a change a Loco operator made in an organization they are not a member of. */
export function OperatorTag() {
	return (
		<Badge tone="info" size="sm" data-slot="operator-tag" className="shrink-0">
			Loco admin
		</Badge>
	);
}
