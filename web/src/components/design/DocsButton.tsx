import { ArrowUpRightIcon, BookOpenIcon } from "lucide-react";

import { Button } from "./Button";

export function DocsButton({ href }: { href: string }) {
	return (
		<Button variant="ghost" nativeButton={false} render={<a href={href} target="_blank" rel="noopener noreferrer" />}>
			<BookOpenIcon />
			Docs
			<ArrowUpRightIcon />
		</Button>
	);
}
