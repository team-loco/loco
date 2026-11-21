import { useRef, useState } from "react";
import { toast } from "sonner";

export function useCopy(resetMs = 1200): [string | null, (key: string, text: string, toastText?: string) => void] {
	const [copied, setCopied] = useState<string | null>(null);
	const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const copy = (key: string, text: string, toastText?: string) => {
		navigator.clipboard.writeText(text).then(
			() => {
				if (toastText !== undefined) toast.success(toastText);
				setCopied(key);
				if (timerRef.current !== null) clearTimeout(timerRef.current);
				timerRef.current = setTimeout(() => {
					setCopied(null);
				}, resetMs);
			},
			() => {
				toast.error("Couldn't copy. Allow clipboard access or copy it by hand.");
			},
		);
	};
	return [copied, copy];
}
