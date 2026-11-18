import { createContext, use, useEffect, useRef, useState, type ReactNode } from "react";

import { Dialog, DialogBody, DialogContent, DialogDescription } from "@/components/design/Dialog";
import { Kbd } from "@/components/design/Kbd";

import { modifierKey } from "@/lib/keyboard";
const GROUPS: { title: string; rows: [string[], string][] }[] = [
	{
		title: "General",
		rows: [
			[[modifierKey, "K"], "Search and commands"],
			[[modifierKey, "B"], "Show or hide the sidebar"],
			[["?"], "Keyboard shortcuts"],
			[["Esc"], "Close the open panel or menu"],
		],
	},
	{
		title: "Search and filter bar",
		rows: [
			[["↑", "↓"], "Move through suggestions"],
			[["↵"], "Choose"],
			[["⌫"], "Remove the last filter, when the bar is empty"],
		],
	},
	{
		title: "Logs, traces and events",
		rows: [[["↑", "↓"], "Previous or next item, with its panel open"]],
	},
	{
		title: "Trace viewer",
		rows: [
			[["↑", "↓", "or", "J", "K"], "Previous or next span"],
			[["←", "→"], "Collapse or expand a span"],
			[[modifierKey, "Scroll"], "Zoom around the pointer"],
			[["Shift", "Scroll"], "Pan"],
			[["Double-click"], "Zoom to a span"],
		],
	},
];
const OpenShortcutsContext = createContext<(() => void) | null>(null);

export function useOpenShortcuts() {
	const open = use(OpenShortcutsContext);
	if (open === null) throw new Error("useOpenShortcuts must be used inside KeyboardShortcutsProvider");
	return open;
}

export function KeyboardShortcutsProvider({ children }: { children: ReactNode }) {
	const [open, setOpen] = useState(false);
	const bodyRef = useRef<HTMLDivElement>(null);
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "?" || event.metaKey || event.ctrlKey || event.altKey) return;
			if (
				event.target instanceof Element &&
				event.target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])')
			)
				return;
			event.preventDefault();
			setOpen((was) => !was);
		};
		window.addEventListener("keydown", onKeyDown);
		return () => {
			window.removeEventListener("keydown", onKeyDown);
		};
	}, []);
	return (
		<OpenShortcutsContext
			value={() => {
				setOpen(true);
			}}
		>
			{children}
			<Dialog open={open} onOpenChange={setOpen}>
				<DialogContent
					title="Keyboard shortcuts"
					initialFocus={bodyRef}
					className="kbd-modal w-[560px] *:data-[slot=dialog-header]:items-center *:data-[slot=dialog-header]:border-b *:data-[slot=dialog-header]:border-line *:data-[slot=dialog-header]:pt-4 *:data-[slot=dialog-header]:pr-3.5 *:data-[slot=dialog-header]:pb-3 *:data-[slot=dialog-header]:pl-[22px] **:data-[slot=dialog-title]:text-[18px] **:data-[slot=dialog-title]:tracking-[-0.02em]"
				>
					<DialogDescription className="sr-only">Keys for navigation, search and the trace viewer.</DialogDescription>
					<DialogBody ref={bodyRef} tabIndex={-1} className="gap-1.5 px-[22px] pt-2 pb-5 outline-none">
						{GROUPS.map((group) => (
							<section key={group.title}>
								<h3 className="mt-3.5 mb-1.5 text-xs font-medium text-fg3">{group.title}</h3>
								<dl>
									{group.rows.map(([keys, label]) => (
										<div
											key={label}
											className="flex min-h-[34px] items-center justify-between gap-4 border-b border-line py-1 text-[13.5px] last:border-b-0"
										>
											<dt>{label}</dt>
											<dd className="flex shrink-0 gap-1">
												{keys.map((key) =>
													key === "or" ? (
														<span key={key} className="self-center px-0.5 text-xs text-fg3">
															or
														</span>
													) : (
														<Kbd
															key={key}
															className="h-6 min-w-6 justify-center rounded-[5px] bg-panel2 px-[7px] text-[11.5px] shadow-[inset_0_-1px_0_var(--line2)] inset-ring-0"
														>
															{key}
														</Kbd>
													),
												)}
											</dd>
										</div>
									))}
								</dl>
							</section>
						))}
					</DialogBody>
				</DialogContent>
			</Dialog>
		</OpenShortcutsContext>
	);
}
