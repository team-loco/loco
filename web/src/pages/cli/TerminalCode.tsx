import type { ReactNode } from "react";
import { SquareTerminalIcon } from "lucide-react";

export function TerminalCode({ code, children }: { code: string; children?: ReactNode }) {
	return (
		<div className="flex flex-col gap-2.5 rounded-[10px] bg-panel px-4 py-3.5">
			<span className="flex items-center gap-2 text-[12.5px] text-fg3">
				<SquareTerminalIcon className="size-[15px]" aria-hidden />
				Check that this matches your terminal
			</span>
			<output className="py-1 text-center font-mono text-[26px] tracking-[0.16em] uppercase">{code}</output>
			{children}
		</div>
	);
}
