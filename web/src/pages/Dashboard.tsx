import { Page, PageHeader } from "@/components/design/Page";
import { useBreadcrumbs } from "@/context/ShellContext";

export function Dashboard() {
	useBreadcrumbs("Dashboard");
	return (
		<Page>
			<PageHeader title="Dashboard" />
		</Page>
	);
}
