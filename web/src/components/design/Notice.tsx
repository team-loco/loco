import { InfoIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { cn } from "@/lib/utils";

const inlineCode =
	"[&_code]:rounded-sm [&_code]:bg-background [&_code]:box-decoration-clone [&_code]:px-1 [&_code]:py-px [&_code]:font-mono [&_code]:text-[0.92em] [&_code]:text-foreground";

export function Notice({
	id,
	className,
	children,
}: {
	id?: string | undefined;
	className?: string | undefined;
	children: ReactNode;
}) {
	return (
		<Alert
			id={id}
			role="note"
			className={cn("border-info-fg bg-info-bg px-3.5 py-2.5 text-base text-info-fg *:[svg]:translate-y-0", className)}
		>
			<InfoIcon className="size-[1lh]" />
			<AlertDescription className={cn("text-base text-info-fg", inlineCode)}>{children}</AlertDescription>
		</Alert>
	);
}
