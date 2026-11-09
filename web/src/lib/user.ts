export function userLabel(user: { name: string; email: string }): string {
	return user.name || user.email;
}
