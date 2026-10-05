import { Page, PageHeader } from "@/components/design/Page";
import { useBreadcrumbs } from "@/context/ShellContext";

export function Observability() {
	useBreadcrumbs("Observability");
	return (
		<Page>
			<PageHeader title="Observability" />
		</Page>
	);
}
