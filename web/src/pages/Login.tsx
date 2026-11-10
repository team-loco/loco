import { Navigate, useLocation, useSearchParams } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { nextPathFrom } from "@/auth/next";
import { AppLoading } from "@/context/AppLoader";

import { SignInCard } from "./auth/SignInCard";
import { signInError } from "./auth/sign-in-error";

export function Login() {
	const { isAuthenticated, isPending } = useAuth();
	const [searchParams] = useSearchParams();
	const location = useLocation();

	if (isAuthenticated) {
		return <Navigate to="/dashboard" />;
	}
	if (isPending) {
		return <AppLoading />;
	}

	return (
		<SignInCard
			next={nextPathFrom(location.state)}
			loginHint={searchParams.get("login_hint")}
			error={signInError(location.state)}
		/>
	);
}
