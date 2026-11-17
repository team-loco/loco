import { createContext, use, useEffect, useState, type ReactNode } from "react";

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
		title: "Trace viewer",
		rows: [
			[["↑", "↓"], "Previous or next span"],
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
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "?" || event.metaKey || event.ctrlKey || event.altKey) return;
			if (
				event.target instanceof Element &&
				event.target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])')
			)
				return;
			event.preventDefault();
			setOpen(true);
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
					className="kbd-modal w-[560px] **:data-[slot=dialog-title]:text-[18px]"
				>
					<DialogDescription className="sr-only">Keys for navigation, search and the trace viewer.</DialogDescription>
					<DialogBody className="gap-5 pt-4">
						{GROUPS.map((group) => (
							<section key={group.title}>
								<h3 className="mb-1 text-xs text-fg3">{group.title}</h3>
								<dl>
									{group.rows.map(([keys, label]) => (
										<div
											key={label}
											className="flex min-h-[34px] items-center justify-between gap-4 border-b border-line py-1 text-[13px]"
										>
											<dt>{label}</dt>
											<dd className="flex shrink-0 gap-1">
												{keys.map((key) => (
													<Kbd
														key={key}
														className="h-6 bg-panel2 px-1.5 text-[11.5px] shadow-[inset_0_-1px_0_var(--line2)]"
													>
														{key}
													</Kbd>
												))}
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
