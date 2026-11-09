import { CheckIcon, CopyIcon } from "lucide-react";

import { useCopy } from "@/hooks/useCopy";

import { Button } from "./Button";

const COPY_KEY = "copy";

export function CopyButton({ text, label = "Copy", name }: { text: string; label?: string; name?: string }) {
	const [copied, copy] = useCopy();
	return (
		<Button
			variant="outline"
			size="sm"
			aria-label={copied === null ? name : "Copied"}
			onClick={() => {
				copy(COPY_KEY, text);
			}}
		>
			{copied === null ? <CopyIcon /> : <CheckIcon className="text-ok-fg" />}
			{copied === null ? label : "Copied"}
		</Button>
	);
}
