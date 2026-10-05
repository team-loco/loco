import { Page, PageHeader } from "@/components/design/Page";
import { useBreadcrumbs } from "@/context/ShellContext";

export function Team() {
	useBreadcrumbs("Team");
	return (
		<Page>
			<PageHeader title="Team" />
		</Page>
	);
}
