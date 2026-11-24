import { useQueryState } from "nuqs";
import { Navigate } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { useSignIn } from "@/auth/SignIn";
import { PageTitle } from "@/components/PageTitle";
import { AppLoading } from "@/context/AppLoader";
import { useMultiTenant } from "@/hooks/useMultiTenant";
import { SignInCard } from "@/pages/auth/SignInCard";

import { Architecture } from "./splash/Architecture";
import { Features } from "./splash/Features";
import { GetStarted } from "./splash/GetStarted";
import { Hero } from "./splash/Hero";
import { Section, SectionTitle } from "./splash/primitives";
import { SplashCtas } from "./splash/SplashCtas";
import { SplashFooter } from "./splash/SplashFooter";
import { SplashNav } from "./splash/SplashNav";

export function Splash() {
	const { isAuthenticated, isPending } = useAuth();
	const { signIn, redirecting } = useSignIn();
	const [loginHint] = useQueryState("login_hint");
	const multiTenant = useMultiTenant();

	if (isAuthenticated) {
		return <Navigate to="/dashboard" replace />;
	}

	if (isPending || multiTenant === undefined) {
		return <AppLoading />;
	}

	// A signed-out visitor, including one who just logged out, starts sign-in with a click.
	if (!multiTenant) {
		return (
			<>
				<PageTitle title="Sign in" />
				<SignInCard next={null} loginHint={loginHint} error={null} autoStart={false} />
			</>
		);
	}

	const startSignIn = () => {
		void signIn(null, loginHint);
	};

	return (
		<div className="flex min-h-screen flex-col overflow-x-clip bg-background text-lg leading-[1.55] text-foreground">
			<SplashNav onSignIn={startSignIn} redirecting={redirecting} />
			<Hero onSignIn={startSignIn} redirecting={redirecting} />
			<GetStarted />
			<Features />
			<Architecture />
			<Section className="flex flex-col items-center pb-[120px] text-center">
				<SectionTitle className="mb-7">Ship something today.</SectionTitle>
				<SplashCtas onSignIn={startSignIn} redirecting={redirecting} />
			</Section>
			<SplashFooter onSignIn={startSignIn} redirecting={redirecting} />
		</div>
	);
}
