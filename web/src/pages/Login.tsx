import { useAuth } from "@/auth/AuthProvider";
import { Navigate } from "react-router";
import { Splash } from "./Splash";

export function Login() {
	const { isAuthenticated } = useAuth();

	if (isAuthenticated) {
		// Will be redirected by DashboardRedirect component from AuthProvider
		return <Navigate to="/dashboard" />;
	}

	return <Splash />;
}
