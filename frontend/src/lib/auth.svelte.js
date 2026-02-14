import { apiEndpoint } from '$lib/config';

export const authState = $state({
	/** @type {boolean} */
	isAuthenticated: false,
	/** @type {boolean} */
	isLoading: true,
	/** @type {{ id: string, name: string } | null} */
	currentUser: null
});

/**
 * Check if user is authenticated by trying to access a protected endpoint
 */
export async function checkAuth() {
	authState.isLoading = true;
	try {
		const response = await fetch(apiEndpoint('/me'), {
			credentials: 'include' // Important: send cookies
		});

		if (response.ok) {
			const user = await response.json();
			authState.isAuthenticated = true;
			authState.currentUser = user;
			// Optionally fetch user info from /users endpoint if needed
		} else {
			authState.isAuthenticated = false;
			authState.currentUser = null;
		}
	} catch (error) {
		console.error('Auth check failed:', error);
		authState.isAuthenticated = false;
		authState.currentUser = null;
	} finally {
		authState.isLoading = false;
	}
}

/**
 * Initiate Strava OAuth login
 */
export function login() {
	const redirectUrl = `${window.location.origin}`;
	window.location.href = apiEndpoint(`/start?redirect_url=${encodeURIComponent(redirectUrl)}`);
}

/**
 * Logout
 */
export async function logout() {
	await fetch(apiEndpoint('/logout'), { credentials: 'include' });
	authState.isAuthenticated = false;
	authState.currentUser = null;
	window.location.href = '/';
}
