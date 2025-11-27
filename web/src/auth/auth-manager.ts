function getCookie(name: string): string | null {
	const value = `; ${document.cookie}`;
	const parts = value.split(`; ${name}=`);
	if (parts.length === 2) return parts.pop()?.split(";").shift() || null;
	return null;
}

export const authManager = {
	getToken(): string | null {
		return getCookie("loco_token");
	},

	isAuthenticated(): boolean {
		return !!this.getToken();
	},
};
