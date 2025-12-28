import { createContext, useContext, useState } from "react";
import type { ReactNode } from "react";

interface HeaderContextType {
	header: ReactNode | null;
	setHeader: (header: ReactNode | null) => void;
}

const HeaderContext = createContext<HeaderContextType | undefined>(undefined);

export function HeaderProvider({ children }: { children: ReactNode }) {
	const [header, setHeader] = useState<ReactNode | null>(null);

	return (
		<HeaderContext.Provider value={{ header, setHeader }}>
			{children}
		</HeaderContext.Provider>
	);
}

// eslint-disable-next-line react-refresh/only-export-components
export function useHeader() {
	const context = useContext(HeaderContext);
	if (!context) {
		throw new Error("useHeader must be used within HeaderProvider");
	}
	return context;
}
