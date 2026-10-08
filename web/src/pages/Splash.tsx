import { Navigate } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { useSignIn } from "@/auth/SignIn";
import { AppLoading } from "@/context/AppLoader";

import { AccessSection } from "./splash/AccessSection";
import { Architecture } from "./splash/Architecture";
import { Features } from "./splash/Features";
import { GetStarted } from "./splash/GetStarted";
import { Hero } from "./splash/Hero";
import { ProductDemo } from "./splash/ProductDemo";
import { SplashFooter } from "./splash/SplashFooter";
import { SplashNav } from "./splash/SplashNav";

export function Splash() {
	const { isAuthenticated, isPending } = useAuth();
	const { signIn, redirecting } = useSignIn();

	if (isAuthenticated) {
		return <Navigate to="/dashboard" replace />;
	}

	if (isPending) {
		return <AppLoading />;
	}

	const startSignIn = () => {
		void signIn(null);
	};

	return (
		<div className="flex min-h-screen flex-col overflow-x-clip bg-background text-lg leading-[1.55] text-foreground">
			<SplashNav onSignIn={startSignIn} redirecting={redirecting} />
			<Hero />
			<ProductDemo />
			<GetStarted />
			<Features />
			<Architecture />
			<AccessSection />
			<SplashFooter onSignIn={startSignIn} redirecting={redirecting} />
		</div>
	);
}
