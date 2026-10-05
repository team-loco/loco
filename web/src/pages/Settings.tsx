import { Page, PageHeader } from "@/components/design/Page";
import { useBreadcrumbs } from "@/context/ShellContext";

export function Settings() {
	useBreadcrumbs("Settings");
	return (
		<Page>
			<PageHeader title="Settings" />
		</Page>
	);
}
