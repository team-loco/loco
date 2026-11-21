import { useRef, useState } from "react";
import { toast } from "sonner";

export function useCopy(resetMs = 1200): [string | null, (key: string, text: string, toastText?: string) => void] {
	const [copied, setCopied] = useState<string | null>(null);
	const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const copy = async (key: string, text: string, toastText?: string) => {
		try {
			// navigator.clipboard is undefined outside secure contexts, so the call itself can throw.
			await navigator.clipboard.writeText(text);
		} catch {
			toast.error("Couldn't copy. Allow clipboard access or copy it by hand.");
			return;
		}
		if (toastText !== undefined) toast.success(toastText);
		setCopied(key);
		if (timerRef.current !== null) clearTimeout(timerRef.current);
		timerRef.current = setTimeout(() => {
			setCopied(null);
		}, resetMs);
	};
	return [
		copied,
		(key, text, toastText) => {
			void copy(key, text, toastText);
		},
	];
}
