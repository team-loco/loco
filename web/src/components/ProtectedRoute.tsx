import { Outlet } from "react-router";
import { ProtectedLayout } from "./layout/ProtectedLayout";

export function ProtectedRoute() {
	return (
		<ProtectedLayout>
			<Outlet />
		</ProtectedLayout>
	);
}
