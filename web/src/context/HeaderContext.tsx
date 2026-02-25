import { createContext, use, useState } from "react";
import type { ReactNode } from "react";

interface HeaderContextType {
	header: ReactNode | null;
	setHeader: (header: ReactNode | null) => void;
}

const HeaderContext = createContext<HeaderContextType | undefined>(undefined);

export function HeaderProvider({ children }: { children: ReactNode }) {
	const [header, setHeader] = useState<ReactNode | null>(null);

	return (
		<HeaderContext value={{ header, setHeader }}>
			{children}
		</HeaderContext>
	);
}

// eslint-disable-next-line react-refresh/only-export-components
export function useHeader() {
	const context = use(HeaderContext);
	if (!context) {
		throw new Error("useHeader must be used within HeaderProvider");
	}
	return context;
}
