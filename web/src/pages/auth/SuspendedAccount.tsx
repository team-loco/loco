import { AuthCard } from "./AuthCard";

export function SuspendedAccount() {
	return (
		<AuthCard title="Your account is suspended">
			<p role="alert" className="m-0 text-center text-md leading-relaxed text-fg3">
				You can’t sign in while your account is suspended. Contact your Loco administrator to restore access.
			</p>
		</AuthCard>
	);
}
