import { apiEndpoint } from '$lib/config';
import type { Route } from '$lib/types/route';

export type BasemapKey = 'graybeard' | 'colorful' | 'neutrino';

export const routesState = $state({
	routes: [] as Route[],
	availableRoutes: [] as Route[],
	isLoadingRoutes: false,
	focusedRouteId: null as number | null,
	isSidebarOpen: true,
	selectedRoutesVisible: true,
	selectedBasemap: 'graybeard' as BasemapKey
});

export async function loadRoutes(userID: number | undefined) {
	if (!userID) {
		routesState.availableRoutes = [];
		return;
	}

	routesState.isLoadingRoutes = true;
	try {
		const res = await fetch(apiEndpoint('/route_details'), { credentials: 'include' });
		const data: unknown = await res.json();
		routesState.availableRoutes = Array.isArray(data) ? data : [];
	} catch {
		routesState.availableRoutes = [];
	} finally {
		routesState.isLoadingRoutes = false;
	}
}
