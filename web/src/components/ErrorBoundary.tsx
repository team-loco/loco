import { Component, type ErrorInfo, type ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { ErrorCard } from "@/components/ErrorCard";

interface ErrorBoundaryState {
	error: unknown;
}

export class ErrorBoundary extends Component<{ children: ReactNode }, ErrorBoundaryState> {
	override state: ErrorBoundaryState = { error: null };

	static getDerivedStateFromError(error: unknown): ErrorBoundaryState {
		return { error };
	}

	override componentDidCatch(error: unknown, info: ErrorInfo) {
		console.error("Unhandled render error", error, info.componentStack);
	}

	override render(): ReactNode {
		if (this.state.error === null) return this.props.children;
		return (
			<div className="flex min-h-screen flex-col items-center justify-center gap-3">
				<ErrorCard error={this.state.error} fallbackMessage="Something went wrong" minHeight="min-h-0" />
				<Button
					variant="outline"
					onClick={() => {
						window.location.reload();
					}}
				>
					Reload
				</Button>
			</div>
		);
	}
}
