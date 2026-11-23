import { useAuth } from "@/auth/AuthProvider";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";

export function LogoutConfirm({ open, onClose }: { open: boolean; onClose: () => void }) {
	const { logout } = useAuth();
	return (
		<ConfirmDialog
			open={open}
			title="Log out?"
			description="You will need to sign in again to use Loco in this browser."
			confirmLabel="Log out"
			confirmVariant="default"
			onConfirm={() => {
				void logout();
			}}
			onClose={onClose}
		/>
	);
}
