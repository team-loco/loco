import { Page, PageHeader } from "@/components/design/Page";
import { useBreadcrumbs } from "@/context/ShellContext";

export function Tokens() {
	useBreadcrumbs("Tokens");
	return (
		<Page>
			<PageHeader title="Tokens" />
		</Page>
	);
}
