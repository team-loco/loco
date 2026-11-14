import { createContext, use, useState } from "react";

import {
	Sidebar as SidebarBase,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupLabel as SidebarGroupLabelBase,
	SidebarHeader,
	SidebarInset as SidebarInsetBase,
	SidebarMenu,
	SidebarMenuButton as SidebarMenuButtonBase,
	SidebarMenuItem,
	SidebarProvider as SidebarProviderBase,
	useSidebar,
} from "@/components/ui/sidebar";
import { cn } from "@/lib/utils";

const motion = "duration-[220ms] ease-[cubic-bezier(0.2,0.7,0.2,1)]";

const WIDTH_MIN = 200;
const WIDTH_MAX = 360;
const WIDTH_DEFAULT = 232;
const WIDTH_KEY = "loco-sidebar-width";
const KEY_STEP = 8;

function readWidth(): number {
	try {
		const saved = Number(localStorage.getItem(WIDTH_KEY));
		return Number.isFinite(saved) && saved >= WIDTH_MIN && saved <= WIDTH_MAX ? saved : WIDTH_DEFAULT;
	} catch {
		return WIDTH_DEFAULT;
	}
}

function saveWidth(width: number) {
	try {
		if (width === WIDTH_DEFAULT) localStorage.removeItem(WIDTH_KEY);
		else localStorage.setItem(WIDTH_KEY, String(width));
	} catch {
		// Storage is unavailable (private mode); the width just won't persist.
	}
}

interface SidebarWidth {
	width: number;
	setWidth: (width: number) => void;
	setResizing: (resizing: boolean) => void;
}

const SidebarWidthContext = createContext<SidebarWidth | null>(null);

function SidebarProvider({ style, className, ...props }: React.ComponentProps<typeof SidebarProviderBase>) {
	const [width, setWidth] = useState(readWidth);
	const [resizing, setResizing] = useState(false);
	const resize = (next: number) => {
		const clamped = Math.min(WIDTH_MAX, Math.max(WIDTH_MIN, Math.round(next)));
		setWidth(clamped);
		saveWidth(clamped);
	};
	return (
		<SidebarWidthContext value={{ width, setWidth: resize, setResizing }}>
			<SidebarProviderBase
				style={{ "--sidebar-width": `${width}px`, ...style } as React.CSSProperties}
				data-resizing={resizing}
				className={cn(
					"[&_[data-slot=sidebar-gap]]:duration-[220ms] [&_[data-slot=sidebar-gap]]:ease-[cubic-bezier(0.2,0.7,0.2,1)]",
					"data-[resizing=true]:[&_[data-slot=sidebar-container]]:transition-none data-[resizing=true]:[&_[data-slot=sidebar-gap]]:transition-none",
					className,
				)}
				{...props}
			/>
		</SidebarWidthContext>
	);
}

/** A drag handle on the sidebar's right edge; the pointer is captured so the drag survives leaving the 7px strip. */
function SidebarResizer() {
	const ctx = use(SidebarWidthContext);
	const [drag, setDrag] = useState<{ startX: number; startWidth: number } | null>(null);
	if (ctx === null) return null;
	const { width, setWidth, setResizing } = ctx;
	const end = () => {
		setDrag(null);
		setResizing(false);
	};
	return (
		<div
			role="separator"
			aria-orientation="vertical"
			aria-label="Resize sidebar"
			aria-valuenow={width}
			aria-valuemin={WIDTH_MIN}
			aria-valuemax={WIDTH_MAX}
			tabIndex={0}
			data-dragging={drag !== null}
			className="absolute inset-y-0 right-0 z-10 w-[7px] cursor-col-resize touch-none outline-none after:absolute after:inset-y-0 after:right-0 after:w-0.5 after:bg-transparent after:transition-colors after:duration-[140ms] hover:after:bg-fg/20 focus-visible:after:bg-fg/20 data-[dragging=true]:after:bg-fg/20"
			onPointerDown={(e) => {
				e.currentTarget.setPointerCapture(e.pointerId);
				setDrag({ startX: e.clientX, startWidth: width });
				setResizing(true);
			}}
			onPointerMove={(e) => {
				if (drag !== null) setWidth(drag.startWidth + e.clientX - drag.startX);
			}}
			onPointerUp={end}
			onPointerCancel={end}
			onDoubleClick={() => {
				setWidth(WIDTH_DEFAULT);
			}}
			onKeyDown={(e) => {
				const next =
					e.key === "ArrowLeft"
						? width - KEY_STEP
						: e.key === "ArrowRight"
							? width + KEY_STEP
							: e.key === "Home"
								? WIDTH_MIN
								: e.key === "End"
									? WIDTH_MAX
									: null;
				if (next === null) return;
				e.preventDefault();
				setWidth(next);
			}}
		/>
	);
}

function Sidebar({ className, children, ...props }: React.ComponentProps<typeof SidebarBase>) {
	return (
		<SidebarBase variant="inset" collapsible="offcanvas" className={cn(motion, "z-30 p-0", className)} {...props}>
			{children}
			<SidebarResizer />
		</SidebarBase>
	);
}

function SidebarInset({ className, ...props }: React.ComponentProps<typeof SidebarInsetBase>) {
	return (
		<SidebarInsetBase
			className={cn(
				"min-w-0 bg-background transition-[margin] md:peer-data-[variant=inset]:rounded-overlay md:peer-data-[variant=inset]:border md:peer-data-[variant=inset]:border-line",
				motion,
				className,
			)}
			{...props}
		/>
	);
}

function SidebarGroupLabel({ className, ...props }: React.ComponentProps<typeof SidebarGroupLabelBase>) {
	return (
		<SidebarGroupLabelBase
			className={cn("h-auto rounded-sm px-2.5 pt-[18px] pb-1.5 text-[12px] font-normal text-fg3", className)}
			{...props}
		/>
	);
}

function SidebarMenuButton({ className, ...props }: React.ComponentProps<typeof SidebarMenuButtonBase>) {
	return (
		<SidebarMenuButtonBase
			className={cn(
				"h-8 gap-2.5 rounded-[5px] px-2.5 text-[13.5px] text-fg2 transition-colors duration-150 ease-out hover:bg-shell-hover hover:text-fg2 active:bg-shell-hover active:text-fg2 data-active:bg-shell-active data-active:font-medium data-active:text-foreground data-active:hover:bg-shell-active data-active:hover:text-foreground [&_svg]:size-4 [&_svg]:text-fg3 data-active:[&_svg]:text-foreground",
				className,
			)}
			{...props}
		/>
	);
}

export {
	Sidebar,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupLabel,
	SidebarHeader,
	SidebarInset,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarProvider,
	useSidebar,
};
