import { Page, PageHeader } from "@/components/design/Page";
import { useBreadcrumbs } from "@/context/ShellContext";

export function Resource() {
	useBreadcrumbs("Resource");
	return (
		<Page>
			<PageHeader title="Resource" />
		</Page>
	);
}
