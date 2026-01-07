import { useAuth } from "@/auth/AuthProvider";
import { Navigate } from "react-router";
import { Splash } from "./Splash";

export function Login() {
	const { isAuthenticated } = useAuth();

	if (isAuthenticated) {
		return <Navigate to="/dashboard" />;
	}

	return <Splash />;
}
