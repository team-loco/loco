import { createContext, use, useEffect, useState, type ReactNode } from "react";

interface ShellContextType {
	crumbs: string[];
	setCrumbs: (crumbs: string[]) => void;
}

const ShellContext = createContext<ShellContextType | null>(null);

export function ShellProvider({ children }: { children: ReactNode }) {
	const [crumbs, setCrumbs] = useState<string[]>([]);
	return <ShellContext value={{ crumbs, setCrumbs }}>{children}</ShellContext>;
}

export function useShellCrumbs(): string[] {
	const ctx = use(ShellContext);
	return ctx?.crumbs ?? [];
}

export function useBreadcrumbs(...crumbs: string[]) {
	const ctx = use(ShellContext);
	const setCrumbs = ctx?.setCrumbs;
	const key = crumbs.join("\u0000");
	useEffect(() => {
		if (!setCrumbs) return;
		setCrumbs(key === "" ? [] : key.split("\u0000"));
		return () => {
			setCrumbs([]);
		};
	}, [key, setCrumbs]);
}
