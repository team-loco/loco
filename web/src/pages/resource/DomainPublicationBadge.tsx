import { PublicationState, type DomainPublication } from "@gen/loco/domain/v1/domain_pb";
import { Badge, type BadgeTone } from "@/components/design/Badge";

export function DomainPublicationBadge({ publication }: { publication: DomainPublication | undefined }) {
	let label: string;
	let tone: BadgeTone;
	switch (publication?.state) {
		case PublicationState.OPERATOR_MANAGED:
			label = "Operator-managed routing";
			tone = "muted";
			break;
		case PublicationState.PUBLISHED:
			label = "Routing published";
			tone = "ok";
			break;
		case PublicationState.ERROR:
			label = "Routing error";
			tone = "bad";
			break;
		case PublicationState.PENDING:
			label = "Routing pending";
			tone = "warn";
			break;
		case PublicationState.UNSPECIFIED:
		case undefined:
			label = "Routing unknown";
			tone = "muted";
	}
	return (
		<Badge size="sm" tone={tone} title={publication?.message ?? undefined}>
			{label}
		</Badge>
	);
}
