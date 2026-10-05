import { createContext, Suspense, use, useRef, useState, type ReactNode } from "react";

import { SidebarInset, SidebarProvider } from "@/components/design/Sidebar";
import { TooltipProvider } from "@/components/design/Tooltip";
import { AppLoading } from "@/context/AppLoader";
import { ShellProvider } from "@/context/ShellContext";

import { AppSidebar } from "./AppSidebar";
import { TopBar } from "./TopBar";

const PEEK_DELAY_MS = 60;

interface SidebarPeek {
	pinned: boolean;
	peeking: boolean;
	onEnter: () => void;
	onLeave: () => void;
	lock: (locked: boolean) => void;
}

const SidebarPeekContext = createContext<SidebarPeek | null>(null);

export function useSidebarPeek(): SidebarPeek {
	const ctx = use(SidebarPeekContext);
	if (!ctx) throw new Error("useSidebarPeek must be used inside AppShell");
	return ctx;
}

function readPinned(): boolean {
	const match = /(?:^|;\s*)sidebar_state=(true|false)/.exec(document.cookie);
	return match?.[1] !== "false";
}

export function AppShell({ children }: { children: ReactNode }) {
	const [pinned, setPinned] = useState(readPinned);
	const [peeking, setPeeking] = useState(false);
	const timer = useRef<number | undefined>(undefined);
	const hovering = useRef(false);
	const locked = useRef(false);

	const clearTimer = () => {
		window.clearTimeout(timer.current);
		timer.current = undefined;
	};

	const peek: SidebarPeek = {
		pinned,
		peeking,
		onEnter: () => {
			hovering.current = true;
			if (pinned) return;
			clearTimer();
			timer.current = window.setTimeout(() => {
				setPeeking(true);
			}, PEEK_DELAY_MS);
		},
		onLeave: () => {
			hovering.current = false;
			clearTimer();
			if (!locked.current) setPeeking(false);
		},
		lock: (next) => {
			locked.current = next;
			if (!next && !hovering.current) setPeeking(false);
		},
	};

	const onOpenChange = (open: boolean) => {
		clearTimer();
		setPeeking(false);
		setPinned(open);
	};

	return (
		<ShellProvider>
			<TooltipProvider>
				<SidebarPeekContext value={peek}>
					<SidebarProvider
						open={pinned || peeking}
						onOpenChange={onOpenChange}
						data-peek={peeking && !pinned}
						className="data-[peek=true]:[&_[data-slot=sidebar-container]]:shadow-drawer data-[peek=true]:[&_[data-slot=sidebar-gap]]:w-(--sidebar-width-icon)"
					>
						<AppSidebar />
						<SidebarInset className="min-w-0 bg-background">
							<TopBar />
							<main className="flex min-w-0 flex-1 flex-col">
								<Suspense fallback={<AppLoading />}>{children}</Suspense>
							</main>
						</SidebarInset>
					</SidebarProvider>
				</SidebarPeekContext>
			</TooltipProvider>
		</ShellProvider>
	);
}
