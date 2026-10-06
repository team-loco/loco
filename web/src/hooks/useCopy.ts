import { useRef, useState } from "react";

export function useCopy(resetMs = 1200): [string | null, (key: string, text: string) => void] {
	const [copied, setCopied] = useState<string | null>(null);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const copy = (key: string, text: string) => {
		void navigator.clipboard.writeText(text).then(() => {
			setCopied(key);
			if (timer.current !== null) clearTimeout(timer.current);
			timer.current = setTimeout(() => {
				setCopied(null);
			}, resetMs);
		});
	};
	return [copied, copy];
}
