import { useState } from "react";
import { Navigate, useLocation } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { LoginModal } from "@/components/LoginModal";
import { AppLoading } from "@/context/AppLoader";

import { AccessSection } from "./splash/AccessSection";
import { Architecture } from "./splash/Architecture";
import { Features } from "./splash/Features";
import { GetStarted } from "./splash/GetStarted";
import { Hero } from "./splash/Hero";
import { ProductDemo } from "./splash/ProductDemo";
import { SplashFooter } from "./splash/SplashFooter";
import { SplashNav } from "./splash/SplashNav";

function wantsSignIn(state: unknown): boolean {
	return typeof state === "object" && state !== null && "signIn" in state && state.signIn === true;
}

function nextFrom(state: unknown): string | null {
	if (typeof state !== "object" || state === null || !("next" in state) || typeof state.next !== "string") return null;
	return state.next.startsWith("/") && !state.next.startsWith("//") ? state.next : null;
}

function oauthErrorFrom(state: unknown): string | null {
	if (typeof state !== "object" || state === null || !("oauthError" in state)) return null;
	return typeof state.oauthError === "string" ? state.oauthError : null;
}

export function Splash() {
	const { isAuthenticated, isPending } = useAuth();
	const location = useLocation();
	const oauthError = oauthErrorFrom(location.state);
	const next = nextFrom(location.state);
	const [loginModalOpen, setLoginModalOpen] = useState(oauthError !== null || wantsSignIn(location.state));

	if (isAuthenticated) {
		return <Navigate to={next ?? "/dashboard"} replace />;
	}

	if (isPending) {
		return <AppLoading />;
	}

	const openLogin = () => {
		setLoginModalOpen(true);
	};

	return (
		<div className="flex min-h-screen flex-col overflow-x-clip bg-background text-lg leading-[1.55] text-foreground">
			<SplashNav onSignIn={openLogin} />
			<Hero />
			<ProductDemo />
			<GetStarted />
			<Features />
			<Architecture />
			<AccessSection />
			<SplashFooter onSignIn={openLogin} />
			<LoginModal open={loginModalOpen} onOpenChange={setLoginModalOpen} initialError={oauthError} next={next} />
		</div>
	);
}
