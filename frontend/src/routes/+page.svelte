<script lang="ts">
	import RouteList from '$lib/RouteList.svelte';
	import { routesState } from '$lib/state.svelte';
	import { getBaseStyle } from '$lib/mapStyle.svelte';
	import { tileServerEndpoint } from '$lib/config';
	import { checkAuth, login, logout, authState } from '$lib/auth.svelte';

	import { MapLibre } from 'svelte-maplibre-gl';
	import type { StyleSpecification } from 'maplibre-gl';

	// Check authentication on page load
	$effect(() => {
		checkAuth();
	});

	// Reactive map style that updates when user changes
	let mapStyle = $state<StyleSpecification | null>(null);

	// Load base map style once
	$effect(() => {
		getBaseStyle().then((style) => {
			mapStyle = style as StyleSpecification;
		});
	});

	// Derived style that updates AllRoutes URL and filters routes when user/selection changes
	let activeMapStyle = $derived.by(() => {
		if (!mapStyle) return null;

		// Deep clone the base style to avoid mutations
		const style = JSON.parse(JSON.stringify(mapStyle));

		// Update the AllRoutes source URL based on userID
		if (authState.isAuthenticated && style.sources && style.sources.AllRoutes) {
			style.sources.AllRoutes.url = tileServerEndpoint(`/data/${authState.currentUser?.id}.json`);
		}

		// Add filter to Route layer based on selected route IDs
		if (style.layers) {
			const routeLayer = style.layers.find((layer) => layer.id === 'Route');
			if (routeLayer && routesState.routes.length > 0) {
				// Filter to show only selected routes
				routeLayer.filter = [
					'all',
					['in', 'id', ...routesState.routes.map((routeId) => String(routeId))]
				];
			} else if (routeLayer) {
				// Remove filter if no routes selected
				delete routeLayer.filter;
			}
		}

		return style;
	});
</script>

<div class="flex h-screen w-full">
	<div
		class="flex h-screen w-sm flex-col space-y-4 overflow-hidden border-r border-gray-300 bg-gray-100 p-4"
	>
		{#if authState.isAuthenticated}
			<div class="flex items-center justify-between">
				<p class="text-gray-700">
					Logged in as <strong>{authState.currentUser?.name}</strong>
				</p>
				<button class="rounded bg-red-500 px-3 py-1 text-white hover:bg-red-600" onclick={logout}>
					Logout
				</button>
			</div>
			<div class="h-full min-h-0 flex-1 overflow-auto">
				<RouteList userID={authState.currentUser?.id} />
			</div>
		{:else}
			<div class="flex items-center justify-end">
				<button
					class="rounded bg-green-500 px-3 py-1 text-white hover:bg-green-600"
					onclick={login}
				>
					Login
				</button>
			</div>
		{/if}
	</div>

	<div class="min-h-0 flex-1">
		{#if activeMapStyle}
			<MapLibre class="h-full w-full" style={activeMapStyle} />
		{:else}
			<div class="flex h-full w-full items-center justify-center bg-gray-200">
				<p class="text-gray-600">Loading map...</p>
			</div>
		{/if}
	</div>
</div>
